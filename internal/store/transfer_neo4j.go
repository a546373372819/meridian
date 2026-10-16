package store

import "github.com/neo4j/neo4j-go-driver/v4/neo4j"


type TransferNeo4jStore struct {
	driver neo4j.Driver
	dbName string
}

func NewTransferNeo4jStore(driver neo4j.Driver, dbName string) *TransferNeo4jStore {
	return &TransferNeo4jStore{driver: driver, dbName: dbName}
}

func (n *TransferNeo4jStore) ApplyBorrow(from, to, resource string, fromVal, toVal, amount float64, elastic bool) error {
	return n.inTx(func(tx neo4j.Transaction) error {
		if err := run(tx, setQuotaCypher(resource), map[string]any{"id": from, "quota": fromVal}); err != nil {
			return err
		}
		if err := run(tx, setQuotaCypher(resource), map[string]any{"id": to, "quota": toVal}); err != nil {
			return err
		}
		return run(tx, recordLoanCypher, map[string]any{"from": from, "to": to, "resource": resource, "amount": amount, "elastic": elastic})
	})
}

func (n *TransferNeo4jStore) ApplyReturn(from, to, resource string, fromVal, toVal, amount float64, elastic bool) error {
	return n.inTx(func(tx neo4j.Transaction) error {
		if err := run(tx, setQuotaCypher(resource), map[string]any{"id": from, "quota": fromVal}); err != nil {
			return err
		}
		if err := run(tx, setQuotaCypher(resource), map[string]any{"id": to, "quota": toVal}); err != nil {
			return err
		}
		return run(tx, reduceLoanCypher, map[string]any{"from": from, "to": to, "resource": resource, "amount": amount, "elastic": elastic})
	})
}

func (n *TransferNeo4jStore) ApplyNamespaceBorrow(fromNamespace, to, resource string, toVal, amount float64) error {
	return n.inTx(func(tx neo4j.Transaction) error {
		if err := run(tx, setQuotaCypher(resource), map[string]any{"id": to, "quota": toVal}); err != nil {
			return err
		}
		return run(tx, recordLoanCypher, map[string]any{"from": fromNamespace, "to": to, "resource": resource, "amount": amount, "elastic": false})
	})
}

func (n *TransferNeo4jStore) ApplyNamespaceReturn(fromNamespace, to, resource string, toVal, amount float64, elastic bool) error {
	return n.inTx(func(tx neo4j.Transaction) error {
		if err := run(tx, setQuotaCypher(resource), map[string]any{"id": to, "quota": toVal}); err != nil {
			return err
		}
		return run(tx, reduceLoanCypher, map[string]any{"from": fromNamespace, "to": to, "resource": resource, "amount": amount, "elastic": elastic})
	})
}

func (n *TransferNeo4jStore) ApplyPoolBorrow(fromEntity, to, resource string, toVal, amount float64) error {
	return n.inTx(func(tx neo4j.Transaction) error {
		if err := run(tx, setQuotaCypher(resource), map[string]any{"id": to, "quota": toVal}); err != nil {
			return err
		}
		return run(tx, recordLoanCypher, map[string]any{"from": fromEntity, "to": to, "resource": resource, "amount": amount, "elastic": true})
	})
}

// moves a loan borrower owes from oldLender onto newLender
func (n *TransferNeo4jStore) ApplyLoanRepoint(oldLender, newLender, borrower, resource string, amount float64, oldElastic bool) error {
	return n.inTx(func(tx neo4j.Transaction) error {
		if err := run(tx, reduceLoanCypher, map[string]any{"from": oldLender, "to": borrower, "resource": resource, "amount": amount, "elastic": oldElastic}); err != nil {
			return err
		}
		return run(tx, recordLoanCypher, map[string]any{"from": newLender, "to": borrower, "resource": resource, "amount": amount, "elastic": true})
	})
}

func (n *TransferNeo4jStore) inTx(work func(neo4j.Transaction) error) error {
	session := startSession(n.driver, n.dbName)
	defer endSession(session)
	_, err := session.WriteTransaction(func(tx neo4j.Transaction) (any, error) {
		return nil, work(tx)
	})
	return err
}

func run(tx neo4j.Transaction, cypher string, params map[string]any) error {
	res, err := tx.Run(cypher, params)
	if err != nil {
		return err
	}
	_, err = res.Consume()
	return err
}
