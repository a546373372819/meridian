package store

import (
	"fmt"
	"log"
	"math"

	"github.com/c12s/meridian/internal/domain"
	"github.com/neo4j/neo4j-go-driver/v4/neo4j"
)

type resourceQuotaNeo4jStore struct {
	driver neo4j.Driver
	dbName string
}

func NewResourceQuotaNeo4jStore(driver neo4j.Driver, dbName string) domain.ResourceQuotaStore {
	if driver == nil {
		log.Fatalln("driver while initializing app neo4j store")
	}
	return &resourceQuotaNeo4jStore{
		driver: driver,
		dbName: dbName,
	}
}

// validates and writes an entitys quotas
func (n *resourceQuotaNeo4jStore) SetResourceQuotas(entityId string, quotas domain.ResourceQuotas, tx neo4j.Transaction) error {
	for resource, quota := range quotas {
		if quota < 0 || math.IsNaN(quota) || math.IsInf(quota, 0) {
			return domain.Invalid("quota for %s must be a finite number >= 0, got %g", resource, quota)
		}
	}

	owned := tx == nil
	if owned {
		session := startSession(n.driver, n.dbName)
		defer endSession(session)
		newTx, err := session.BeginTransaction()
		if err != nil {
			return err
		}
		defer newTx.Close()
		tx = newTx
	}

	total, err := n.getQuotas(tx, entityId)
	if err != nil {
		return err
	}

	parentEntityId, err := n.getParentEntity(tx, entityId)
	if err != nil {
		log.Println(err)
	} else {

		availableParent, err := n.GetEffectiveAvailableResources(tx, parentEntityId)
		if err != nil {
			return err
		}
		for resource, quota := range quotas {
			if available := availableParent[resource] + total[resource]; available < quota {
				return domain.Precondition("requested %f quota for the resource %s, but only %f available in parent", quota, resource, available)
			}
		}
	}

	committed, err := n.committedResources(tx, entityId, total)
	if err != nil {
		return err
	}
	for resource, quota := range quotas {
		if c := committed[resource]; c > quota {
			return domain.Precondition("requested %f quota for the resource %s, but %f already committed", quota, resource, c)
		}
	}

	if err := n.setResourceQuotas(tx, entityId, quotas); err != nil {
		return err
	}
	if owned {
		return tx.Commit()
	}
	return nil
}

// the minimum quota an entity can be set to, loan aware
func (n *resourceQuotaNeo4jStore) committedResources(tx neo4j.Transaction, entityId string, total domain.ResourceQuotas) (domain.ResourceQuotas, error) {
	if domain.IsAppId(entityId) {
		return borrowedByTx(tx, entityId)
	}
	effective, err := n.GetEffectiveAvailableResources(tx, entityId)
	if err != nil {
		return nil, err
	}
	committed := make(domain.ResourceQuotas)
	for _, resource := range domain.SupportedResourceQuotas {
		if avail, ok := effective[resource]; ok {
			committed[resource] = total[resource] - avail
		}
	}
	return committed, nil
}

// returns the id of every app (org/ns/app) in the graph
func (n *resourceQuotaNeo4jStore) ListAppIds() ([]string, error) {
	session := startSession(n.driver, n.dbName)
	defer endSession(session)
	res, err := session.Run(listEntityIdsCypher, nil)
	if err != nil {
		return nil, err
	}
	records, err := res.Collect()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, rec := range records {
		idAny, _ := rec.Get("id")
		if id, ok := idAny.(string); ok && domain.IsAppId(id) {
			out = append(out, id)
		}
	}
	return out, nil
}

// returns every entity id
const listEntityIdsCypher = `
MATCH (a:Entity)
RETURN a.id AS id;
`

func (n *resourceQuotaNeo4jStore) GetResourceQuotas(entityId string) (domain.ResourceQuotas, error) {
	session := startSession(n.driver, n.dbName)
	defer endSession(session)
	tx, err := session.BeginTransaction()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return n.getQuotas(tx, entityId)
}

func (n *resourceQuotaNeo4jStore) getQuotas(tx neo4j.Transaction, id string) (domain.ResourceQuotas, error) {
	res, err := tx.Run(getEntityCypher, map[string]any{
		"id": id,
	})
	if err != nil {
		return nil, err
	}
	return n.readQuotas(res)
}

func (n *resourceQuotaNeo4jStore) getParentEntity(tx neo4j.Transaction, entityId string) (string, error) {
	res, err := tx.Run(getParentEntityCypher, map[string]any{
		"id": entityId,
	})
	if err != nil {
		return "", err
	}
	return n.readEntityId(res)
}

// unallocated budget of an entity, loan-aware: its total minus what each child
// really consumes (child.total - borrowedIn(child) + borrowedOut(child)),
// adjusted for what the entity itself borrowed/lent
func (n *resourceQuotaNeo4jStore) GetEffectiveAvailableResources(tx neo4j.Transaction, entityId string) (domain.ResourceQuotas, error) {
	if tx == nil {
		session := startSession(n.driver, n.dbName)
		defer endSession(session)
		newTx, err := session.BeginTransaction()
		if err != nil {
			return nil, err
		}
		defer newTx.Close()
		tx = newTx
	}

	quotas := make(domain.ResourceQuotas)
	for _, resourceName := range domain.SupportedResourceQuotas {
		res, err := tx.Run(getEffectiveAvailableCypher, map[string]any{
			"id":            entityId,
			"resource_name": resourceName,
		})
		if err != nil {
			return nil, err
		}
		records, err := res.Collect()
		if err != nil {
			return nil, err
		}
		if len(records) == 0 || len(records[0].Values) == 0 {
			continue
		}
		if v, ok := records[0].Values[0].(float64); ok {
			quotas[resourceName] = v
		}
	}
	return quotas, nil
}

const getEffectiveAvailableCypher = `
MATCH (n:Entity{id: $id})
OPTIONAL MATCH (n)-[:CHILD]->(d:Entity)
WITH n,
     coalesce(d[$resource_name], 0) AS dq,
     reduce(s = 0.0, x IN [(d)-[b:BORROWED {resource: $resource_name}]->(:Entity) | b.amount] | s + x) AS borrowedIn,
     reduce(s = 0.0, x IN [(:Entity)-[b:BORROWED {resource: $resource_name}]->(d) | b.amount] | s + x) AS borrowedOut
WITH n, collect(dq - borrowedIn + borrowedOut) AS consumptions
WITH n, coalesce(n[$resource_name], 0) - reduce(s = 0.0, c IN consumptions | s + c) AS childAvail
RETURN childAvail
     + reduce(s = 0.0, x IN [(n)-[b:BORROWED {resource: $resource_name}]->(:Entity) | b.amount] | s + x)
     - reduce(s = 0.0, x IN [(:Entity)-[b:BORROWED {resource: $resource_name}]->(n) | b.amount] | s + x)
     AS available;
`

func (n *resourceQuotaNeo4jStore) setResourceQuotas(tx neo4j.Transaction, entityId string, quotas domain.ResourceQuotas) error {
	for resource, quota := range quotas {
		_, err := tx.Run(setQuotaCypher(resource), map[string]any{
			"id":    entityId,
			"quota": quota,
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (n *resourceQuotaNeo4jStore) readEntityId(res neo4j.Result) (string, error) {
	if res.Err() != nil {
		return "", res.Err()
	}
	records, err := res.Collect()
	if err != nil {
		return "", err
	}
	if len(records) == 0 {
		return "", domain.NotFound("entity not found")
	}
	record := records[0]
	propertiesAny, found := record.Get("properties")
	if !found {
		return "", fmt.Errorf("entity has no properties")
	}
	properties, ok := propertiesAny.(map[string]any)
	if !ok {
		return "", fmt.Errorf("entity has no properties")
	}
	idAny, found := properties["id"]
	if !found {
		return "", fmt.Errorf("id not found")
	}
	id, ok := idAny.(string)
	if !ok {
		return "", fmt.Errorf("invalid id type: %v", idAny)
	}
	return id, nil
}

func (n *resourceQuotaNeo4jStore) readQuotas(res neo4j.Result) (domain.ResourceQuotas, error) {
	if res.Err() != nil {
		return nil, res.Err()
	}
	records, err := res.Collect()
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, domain.NotFound("entity not found")
	}
	record := records[0]
	propertiesAny, found := record.Get("properties")
	if !found {
		return nil, fmt.Errorf("entity has no properties")
	}
	properties, ok := propertiesAny.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("entity has no properties")
	}
	quotas := make(domain.ResourceQuotas)
	for _, resourceName := range domain.SupportedResourceQuotas {
		quotaAny, found := properties[resourceName]
		if found {
			if quota, ok := quotaAny.(float64); !ok {
				log.Printf("invalid quota type for resource name %s: %v\n", resourceName, quotaAny)
			} else {
				quotas[resourceName] = quota
			}
		}
	}
	return quotas, nil
}

const getParentEntityCypher = `
MATCH (e:Entity{id: $id})
OPTIONAL MATCH (p:Entity)-[:CHILD]->(e)
RETURN properties(p) AS properties;
`

const getEntityCypher = `
MATCH (e:Entity{id: $id})
RETURN properties(e) AS properties;
`

func setQuotaCypher(resource string) string {
	return fmt.Sprintf("MATCH (e:Entity{id: $id})SET e.%s = $quota;", resource)
}
