package store

import (
	"fmt"
	"log"

	"github.com/c12s/meridian/internal/domain"
	"github.com/neo4j/neo4j-go-driver/v4/neo4j"
)


type loanNeo4jStore struct {
	driver neo4j.Driver
	dbName string
}

func NewLoanNeo4jStore(driver neo4j.Driver, dbName string) domain.LoanStore {
	if driver == nil {
		log.Fatalln("driver nil while initializing loan neo4j store")
	}
	return &loanNeo4jStore{driver: driver, dbName: dbName}
}

func (n *loanNeo4jStore) Outstanding(from, to, resource string, elastic bool) (float64, error) {
	session := startSession(n.driver, n.dbName)
	defer endSession(session)
	res, err := session.Run(outstandingLoanCypher, map[string]any{
		"from": from, "to": to, "resource": resource, "elastic": elastic,
	})
	if err != nil {
		return 0, err
	}
	records, err := res.Collect()
	if err != nil {
		return 0, err
	}
	if len(records) == 0 || len(records[0].Values) == 0 {
		return 0, nil 
	}
	amount, ok := records[0].Values[0].(float64)
	if !ok {
		return 0, nil
	}
	return amount, nil
}

func (n *loanNeo4jStore) BorrowedBy(app, resource string) (float64, error) {
	session := startSession(n.driver, n.dbName)
	defer endSession(session)
	tx, err := session.BeginTransaction()
	if err != nil {
		return 0, err
	}
	defer tx.Close()
	held, err := borrowedByTx(tx, app)
	if err != nil {
		return 0, err
	}
	return held[resource], nil
}

// sums everything entityId holds on loan per resource
func borrowedByTx(tx neo4j.Transaction, entityId string) (domain.ResourceQuotas, error) {
	res, err := tx.Run(borrowedByCypher, map[string]any{"id": entityId})
	if err != nil {
		return nil, err
	}
	records, err := res.Collect()
	if err != nil {
		return nil, err
	}
	out := make(domain.ResourceQuotas)
	for _, rec := range records {
		resource, _ := rec.Get("resource")
		amount, _ := rec.Get("amount")
		r, ok1 := resource.(string)
		a, ok2 := amount.(float64)
		if ok1 && ok2 {
			out[r] = a
		}
	}
	return out, nil
}

func (n *loanNeo4jStore) ReassignAll(oldApp, newApp string) error {
	session := startSession(n.driver, n.dbName)
	defer endSession(session)
	_, err := session.WriteTransaction(func(tx neo4j.Transaction) (any, error) {
		params := map[string]any{"old": oldApp, "new": newApp}

		res, err := tx.Run(`MATCH (n:Entity{id: $new}) RETURN count(n) AS c`, params)
		if err != nil {
			return nil, err
		}
		rec, err := res.Single()
		if err != nil {
			return nil, err
		}
		if c, _ := rec.Values[0].(int64); c == 0 {
			return nil, fmt.Errorf("successor %s not found", newApp)
		}
		for _, cypher := range []string{dropMutualLoansCypher, moveBorrowedLoansCypher, moveLentLoansCypher} {
			res, err := tx.Run(cypher, params)
			if err != nil {
				return nil, err
			}
			if _, err := res.Consume(); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	return err
}

func (n *loanNeo4jStore) List() ([]domain.Loan, error) {
	return n.queryLoans(listLoansCypher, nil)
}

func (n *loanNeo4jStore) Involving(id string) ([]domain.Loan, error) {
	return n.queryLoans(involvingLoansCypher, map[string]any{"id": id})
}

// runs a query returning from_id, to_id, resource, amount, elastic rows and reads them as loans
func (n *loanNeo4jStore) queryLoans(cypher string, params map[string]any) ([]domain.Loan, error) {
	session := startSession(n.driver, n.dbName)
	defer endSession(session)
	res, err := session.Run(cypher, params)
	if err != nil {
		return nil, err
	}
	records, err := res.Collect()
	if err != nil {
		return nil, err
	}
	loans := make([]domain.Loan, 0, len(records))
	for _, rec := range records {
		from, _ := rec.Get("from_id")
		to, _ := rec.Get("to_id")
		resource, _ := rec.Get("resource")
		amount, _ := rec.Get("amount")
		elastic, _ := rec.Get("elastic")
		fromStr, ok1 := from.(string)
		toStr, ok2 := to.(string)
		resStr, ok3 := resource.(string)
		amt, ok4 := amount.(float64)
		if !ok1 || !ok2 || !ok3 || !ok4 {
			continue
		}
		el, _ := elastic.(bool)
		loans = append(loans, domain.Loan{From: fromStr, To: toStr, Resource: resStr, Amount: amt, Elastic: el})
	}
	return loans, nil
}



// adds $amount to the loan of kind $elastic, creating the edge
const recordLoanCypher = `
MATCH (f:Entity{id: $from}), (t:Entity{id: $to})
OPTIONAL MATCH (t)-[r:BORROWED{resource: $resource}]->(f)
WHERE coalesce(r.elastic, false) = $elastic
WITH f, t, head(collect(r)) AS existing
FOREACH (_ IN CASE WHEN existing IS NULL THEN [1] ELSE [] END |
  CREATE (t)-[:BORROWED{resource: $resource, amount: $amount, elastic: $elastic}]->(f))
FOREACH (_ IN CASE WHEN existing IS NULL THEN [] ELSE [1] END |
  SET existing.amount = coalesce(existing.amount, 0) + $amount);
`

// reads the loan of kind $elastic.
const outstandingLoanCypher = `
MATCH (t:Entity{id: $to})-[r:BORROWED{resource: $resource}]->(f:Entity{id: $from})
WHERE coalesce(r.elastic, false) = $elastic
RETURN r.amount AS amount;
`

const borrowedByCypher = `
MATCH (e:Entity{id: $id})-[b:BORROWED]->(:Entity)
RETURN b.resource AS resource, sum(b.amount) AS amount;
`

const reduceLoanCypher = `
MATCH (t:Entity{id: $to})-[r:BORROWED{resource: $resource}]->(f:Entity{id: $from})
WHERE coalesce(r.elastic, false) = $elastic
SET r.amount = r.amount - $amount
WITH r WHERE r.amount <= 0
DELETE r;
`

const listLoansCypher = `
MATCH (t:Entity)-[r:BORROWED]->(f:Entity)
RETURN f.id AS from_id, t.id AS to_id, r.resource AS resource, r.amount AS amount,
       coalesce(r.elastic, false) AS elastic;
`

// reads only $ids own BORROWED edges
const involvingLoansCypher = `
MATCH (t:Entity{id: $id})-[r:BORROWED]->(f:Entity)
RETURN f.id AS from_id, t.id AS to_id, r.resource AS resource, r.amount AS amount,
       coalesce(r.elastic, false) AS elastic
UNION ALL
MATCH (t:Entity)-[r:BORROWED]->(f:Entity{id: $id})
RETURN f.id AS from_id, t.id AS to_id, r.resource AS resource, r.amount AS amount,
       coalesce(r.elastic, false) AS elastic;
`

// ReassignAll 1: loans between oldApp and its successor disappear
const dropMutualLoansCypher = `
MATCH (:Entity{id: $old})-[r:BORROWED]-(:Entity{id: $new})
DELETE r;
`

// ReassignAll 2: what oldApp borrowed, the successor now owes
const moveBorrowedLoansCypher = `
MATCH (:Entity{id: $old})-[r:BORROWED]->(f:Entity)
MATCH (n:Entity{id: $new})
WITH r, n, f, r.resource AS res, coalesce(r.elastic, false) AS el, r.amount AS amt
OPTIONAL MATCH (n)-[x:BORROWED{resource: res}]->(f)
WHERE coalesce(x.elastic, false) = el
WITH r, n, f, res, el, amt, head(collect(x)) AS existing
FOREACH (_ IN CASE WHEN existing IS NULL THEN [1] ELSE [] END |
  CREATE (n)-[:BORROWED{resource: res, amount: amt, elastic: el}]->(f))
FOREACH (_ IN CASE WHEN existing IS NULL THEN [] ELSE [1] END |
  SET existing.amount = coalesce(existing.amount, 0) + amt)
DELETE r;
`

// ReassignAll, step 3: what others borrowed from oldApp, they now owe the successor
const moveLentLoansCypher = `
MATCH (t:Entity)-[r:BORROWED]->(:Entity{id: $old})
MATCH (n:Entity{id: $new})
WITH r, n, t, r.resource AS res, coalesce(r.elastic, false) AS el, r.amount AS amt
OPTIONAL MATCH (t)-[x:BORROWED{resource: res}]->(n)
WHERE coalesce(x.elastic, false) = el
WITH r, n, t, res, el, amt, head(collect(x)) AS existing
FOREACH (_ IN CASE WHEN existing IS NULL THEN [1] ELSE [] END |
  CREATE (t)-[:BORROWED{resource: res, amount: amt, elastic: el}]->(n))
FOREACH (_ IN CASE WHEN existing IS NULL THEN [] ELSE [1] END |
  SET existing.amount = coalesce(existing.amount, 0) + amt)
DELETE r;
`
