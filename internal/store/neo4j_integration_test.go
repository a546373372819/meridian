package store

import (
	"context"
	"reflect"
	"sort"
	"os"
	"testing"

	"github.com/c12s/meridian/internal/domain"
	"github.com/c12s/meridian/internal/services"
	"github.com/neo4j/neo4j-go-driver/v4/neo4j"
)

// requires a live disposable Neo4j

const testDB = "neo4j"

func neo4jTestDriver(t *testing.T) neo4j.Driver {
	t.Helper()
	addr := os.Getenv("NEO4J_TEST_ADDR")
	if addr == "" {
		t.Skip("NEO4J_TEST_ADDR not set; skipping tests that wipe the Neo4j database")
	}
	driver, err := neo4j.NewDriver("bolt://"+addr, neo4j.NoAuth())
	if err != nil {
		t.Skipf("neo4j driver: %v", err)
	}
	if err := driver.VerifyConnectivity(); err != nil {
		driver.Close()
		t.Skipf("neo4j not reachable at %s (start it with docker): %v", addr, err)
	}
	return driver
}

func seedGraph(t *testing.T, driver neo4j.Driver) {
	t.Helper()
	s := driver.NewSession(neo4j.SessionConfig{DatabaseName: testDB})
	defer s.Close()
	stmts := []string{
		`MATCH (n) DETACH DELETE n`,
		`MERGE (o:Entity{id:'org1'})      SET o.cpu=100.0, o.mem=0.0, o.disk=0.0`,
		`MERGE (a:Entity{id:'org1/ns1'})  SET a.cpu=4.0,   a.mem=0.0, a.disk=0.0`,
		`MERGE (b:Entity{id:'org1/ns2'})  SET b.cpu=1.0,   b.mem=0.0, b.disk=0.0`,
		`MERGE (p:Entity{id:'org1/ns1/api'}) SET p.cpu=3.0, p.mem=0.0, p.disk=0.0`,
		`MERGE (q:Entity{id:'org1/ns2/db'})  SET q.cpu=2.0, q.mem=0.0, q.disk=0.0`,
		`MATCH (o:Entity{id:'org1'}),(a:Entity{id:'org1/ns1'}) MERGE (o)-[:CHILD]->(a)`,
		`MATCH (o:Entity{id:'org1'}),(b:Entity{id:'org1/ns2'}) MERGE (o)-[:CHILD]->(b)`,
		`MATCH (a:Entity{id:'org1/ns1'}),(p:Entity{id:'org1/ns1/api'}) MERGE (a)-[:CHILD]->(p)`,
		`MATCH (b:Entity{id:'org1/ns2'}),(q:Entity{id:'org1/ns2/db'}) MERGE (b)-[:CHILD]->(q)`,
	}
	for _, c := range stmts {
		res, err := s.Run(c, nil)
		if err != nil {
			t.Fatalf("seed %q: %v", c, err)
		}
		if _, err := res.Consume(); err != nil {
			t.Fatalf("seed consume %q: %v", c, err)
		}
	}
}

// runCypher, recordLoan and reduceLoan write loans with the production queries the way TransferNeo4jStore does inside its transactions
func runCypher(t *testing.T, driver neo4j.Driver, cypher string, params map[string]any) {
	t.Helper()
	sess := driver.NewSession(neo4j.SessionConfig{DatabaseName: testDB})
	defer sess.Close()
	res, err := sess.Run(cypher, params)
	if err != nil {
		t.Fatalf("run cypher: %v", err)
	}
	if _, err := res.Consume(); err != nil {
		t.Fatalf("consume: %v", err)
	}
}

func recordLoan(t *testing.T, driver neo4j.Driver, from, to, resource string, amount float64, elastic bool) {
	t.Helper()
	runCypher(t, driver, recordLoanCypher, map[string]any{
		"from": from, "to": to, "resource": resource, "amount": amount, "elastic": elastic,
	})
}

func reduceLoan(t *testing.T, driver neo4j.Driver, from, to, resource string, amount float64, elastic bool) {
	t.Helper()
	runCypher(t, driver, reduceLoanCypher, map[string]any{
		"from": from, "to": to, "resource": resource, "amount": amount, "elastic": elastic,
	})
}

// loan queries - amounts accumulate per kind, structural and elastic loans between the same pair stay separate, BorrowedBy sums both, and a loan reduced to zero disappears
func TestNeo4jLoanQueries(t *testing.T) {
	driver := neo4jTestDriver(t)
	defer driver.Close()
	seedGraph(t, driver)

	loans := NewLoanNeo4jStore(driver, testDB)
	lender, borrower := "org1/ns1/api", "org1/ns2/db"

	recordLoan(t, driver, lender, borrower, "cpu", 1, false)
	if out, err := loans.Outstanding(lender, borrower, "cpu", false); err != nil || out != 1 {
		t.Fatalf("Outstanding = %g (err %v), want 1", out, err)
	}
	recordLoan(t, driver, lender, borrower, "cpu", 2, false)
	if out, _ := loans.Outstanding(lender, borrower, "cpu", false); out != 3 {
		t.Fatalf("structural after accumulate = %g, want 3", out)
	}

	// elastic loan between the same pair is a separate edge
	recordLoan(t, driver, lender, borrower, "cpu", 2, true)
	if out, _ := loans.Outstanding(lender, borrower, "cpu", false); out != 3 {
		t.Errorf("structural changed by elastic loan: %g, want 3", out)
	}
	if out, _ := loans.Outstanding(lender, borrower, "cpu", true); out != 2 {
		t.Errorf("elastic = %g, want 2", out)
	}
	if l, err := loans.List(); err != nil || len(l) != 2 {
		t.Fatalf("List len = %d (err %v), want 2 (one per kind)", len(l), err)
	}
	if got, _ := loans.BorrowedBy(borrower, "cpu"); got != 5 {
		t.Errorf("BorrowedBy = %g, want 5 (both kinds)", got)
	}
	for _, id := range []string{lender, borrower} {
		if l, err := loans.Involving(id); err != nil || len(l) != 2 {
			t.Errorf("Involving(%s) len = %d (err %v), want 2", id, len(l), err)
		}
	}
	if l, _ := loans.Involving("org1/ns1"); len(l) != 0 {
		t.Errorf("Involving(org1/ns1) = %d loans, want 0 (not a party)", len(l))
	}

	reduceLoan(t, driver, lender, borrower, "cpu", 3, false)
	if out, _ := loans.Outstanding(lender, borrower, "cpu", false); out != 0 {
		t.Errorf("structural after full reduce = %g, want 0", out)
	}
	if out, _ := loans.Outstanding(lender, borrower, "cpu", true); out != 2 {
		t.Errorf("elastic changed by structural reduce: %g, want 2", out)
	}
	if l, _ := loans.List(); len(l) != 1 {
		t.Errorf("List len = %d after reduce, want 1 (zero loan removed)", len(l))
	}
}

func TestNeo4jGetResourceQuotas(t *testing.T) {
	driver := neo4jTestDriver(t)
	defer driver.Close()
	seedGraph(t, driver)

	store := &resourceQuotaNeo4jStore{driver: driver, dbName: testDB}
	q, err := store.GetResourceQuotas("org1/ns1/api")
	if err != nil {
		t.Fatalf("GetResourceQuotas: %v", err)
	}
	if q["cpu"] != 3 {
		t.Errorf("api cpu quota = %g, want 3", q["cpu"])
	}
}


func TestNeo4jBorrowingAwareAvailable(t *testing.T) {
	driver := neo4jTestDriver(t)
	defer driver.Close()
	seedGraph(t, driver)

	store := &resourceQuotaNeo4jStore{driver: driver, dbName: testDB}
	// db (ns2) borrowed 1 cpu from api (ns1).
	recordLoan(t, driver, "org1/ns1/api", "org1/ns2/db", "cpu", 1, false)

	effNs2, err := store.GetEffectiveAvailableResources(nil, "org1/ns2")
	if err != nil {
		t.Fatalf("GetEffectiveAvailableResources ns2: %v", err)
	}
	if effNs2["cpu"] != 0 {
		t.Errorf("effective ns2 available cpu = %g, want 0 (borrow covered by B_in)", effNs2["cpu"])
	}
	effNs1, err := store.GetEffectiveAvailableResources(nil, "org1/ns1")
	if err != nil {
		t.Fatalf("GetEffectiveAvailableResources ns1: %v", err)
	}
	if effNs1["cpu"] != 0 {
		t.Errorf("effective ns1 available cpu = %g, want 0 (lent-out still owned)", effNs1["cpu"])
	}
}

type noopDispatcher struct{}

func (noopDispatcher) DispatchQuotas(_ context.Context, _, _, _ string, _ domain.ResourceQuotas) error {
	return nil
}

// seeds the PRE-borrow state - api (ns1) cpu 4, db (ns2) cpu 1.
func seedInitial(t *testing.T, driver neo4j.Driver) {
	t.Helper()
	s := driver.NewSession(neo4j.SessionConfig{DatabaseName: testDB})
	defer s.Close()
	stmts := []string{
		`MATCH (n) DETACH DELETE n`,
		`MERGE (o:Entity{id:'org1'})      SET o.cpu=100.0, o.mem=0.0, o.disk=0.0`,
		`MERGE (a:Entity{id:'org1/ns1'})  SET a.cpu=4.0,   a.mem=0.0, a.disk=0.0`,
		`MERGE (b:Entity{id:'org1/ns2'})  SET b.cpu=1.0,   b.mem=0.0, b.disk=0.0`,
		`MERGE (p:Entity{id:'org1/ns1/api'}) SET p.cpu=4.0, p.mem=0.0, p.disk=0.0`,
		`MERGE (q:Entity{id:'org1/ns2/db'})  SET q.cpu=1.0, q.mem=0.0, q.disk=0.0`,
		`MATCH (o:Entity{id:'org1'}),(a:Entity{id:'org1/ns1'}) MERGE (o)-[:CHILD]->(a)`,
		`MATCH (o:Entity{id:'org1'}),(b:Entity{id:'org1/ns2'}) MERGE (o)-[:CHILD]->(b)`,
		`MATCH (a:Entity{id:'org1/ns1'}),(p:Entity{id:'org1/ns1/api'}) MERGE (a)-[:CHILD]->(p)`,
		`MATCH (b:Entity{id:'org1/ns2'}),(q:Entity{id:'org1/ns2/db'}) MERGE (b)-[:CHILD]->(q)`,
	}
	for _, c := range stmts {
		res, err := s.Run(c, nil)
		if err != nil {
			t.Fatalf("seed %q: %v", c, err)
		}
		if _, err := res.Consume(); err != nil {
			t.Fatalf("seed consume %q: %v", c, err)
		}
	}
}

// seeds ns1 (cpu 4) with two apps: api (cpu 3) and web (cpu 1).
func seedSameNs(t *testing.T, driver neo4j.Driver) {
	t.Helper()
	s := driver.NewSession(neo4j.SessionConfig{DatabaseName: testDB})
	defer s.Close()
	stmts := []string{
		`MATCH (n) DETACH DELETE n`,
		`MERGE (o:Entity{id:'org1'})         SET o.cpu=100.0, o.mem=0.0, o.disk=0.0`,
		`MERGE (a:Entity{id:'org1/ns1'})     SET a.cpu=4.0,   a.mem=0.0, a.disk=0.0`,
		`MERGE (p:Entity{id:'org1/ns1/api'}) SET p.cpu=3.0,   p.mem=0.0, p.disk=0.0`,
		`MERGE (w:Entity{id:'org1/ns1/web'}) SET w.cpu=1.0,   w.mem=0.0, w.disk=0.0`,
		`MATCH (o:Entity{id:'org1'}),(a:Entity{id:'org1/ns1'}) MERGE (o)-[:CHILD]->(a)`,
		`MATCH (a:Entity{id:'org1/ns1'}),(p:Entity{id:'org1/ns1/api'}) MERGE (a)-[:CHILD]->(p)`,
		`MATCH (a:Entity{id:'org1/ns1'}),(w:Entity{id:'org1/ns1/web'}) MERGE (a)-[:CHILD]->(w)`,
	}
	for _, c := range stmts {
		res, err := s.Run(c, nil)
		if err != nil {
			t.Fatalf("seed %q: %v", c, err)
		}
		if _, err := res.Consume(); err != nil {
			t.Fatalf("seed consume %q: %v", c, err)
		}
	}
}

// checks the reconcilers data source
func TestNeo4jListAppIds(t *testing.T) {
	driver := neo4jTestDriver(t)
	defer driver.Close()
	seedInitial(t, driver) // org1, org1/ns1, org1/ns2, org1/ns1/api, org1/ns2/db

	store := &resourceQuotaNeo4jStore{driver: driver, dbName: testDB}
	ids, err := store.ListAppIds()
	if err != nil {
		t.Fatalf("ListAppIds: %v", err)
	}
	sort.Strings(ids)
	if want := []string{"org1/ns1/api", "org1/ns2/db"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("ListAppIds = %v, want %v", ids, want)
	}
}

// proves the termination trigger against live Neo4j - terminate api (cpu 3) and hand its resources to successor web (cpu 1), which grows to 4 through
func TestNeo4jInheritOnTermination(t *testing.T) {
	driver := neo4jTestDriver(t)
	defer driver.Close()
	seedSameNs(t, driver)

	quotaStore := &resourceQuotaNeo4jStore{driver: driver, dbName: testDB}
	svc := services.NewBorrowService(NewQuotaStoreAccessor(quotaStore), NewLoanNeo4jStore(driver, testDB), NewTransferNeo4jStore(driver, testDB), noopDispatcher{})

	inherited, err := quotaStore.GetResourceQuotas("org1/ns1/api")
	if err != nil {
		t.Fatalf("read terminated app: %v", err)
	}
	s := driver.NewSession(neo4j.SessionConfig{DatabaseName: testDB})
	res, err := s.Run(`MATCH (a:Entity{id:'org1/ns1/api'}) DETACH DELETE a`, nil)
	if err == nil {
		_, err = res.Consume()
	}
	s.Close()
	if err != nil {
		t.Fatalf("delete terminated app: %v", err)
	}

	if err := svc.InheritResources(context.Background(), "org1/ns1/web", inherited); err != nil {
		t.Fatalf("inherit on termination via Neo4j should succeed: %v", err)
	}
	web, _ := quotaStore.GetResourceQuotas("org1/ns1/web")
	if web["cpu"] != 4 {
		t.Errorf("successor web cpu = %g, want 4 (1 + inherited 3)", web["cpu"])
	}
}

// proves the cross-namespace termination

func TestNeo4jCrossNsTerminationBorrow(t *testing.T) {
	driver := neo4jTestDriver(t)
	defer driver.Close()
	seedInitial(t, driver) // api(ns1)=4, db(ns2)=1

	quotaStore := &resourceQuotaNeo4jStore{driver: driver, dbName: testDB}
	loans := NewLoanNeo4jStore(driver, testDB)
	svc := services.NewBorrowService(NewQuotaStoreAccessor(quotaStore), loans, NewTransferNeo4jStore(driver, testDB), noopDispatcher{})

	inherited, err := quotaStore.GetResourceQuotas("org1/ns1/api")
	if err != nil {
		t.Fatalf("read terminated app: %v", err)
	}
	s := driver.NewSession(neo4j.SessionConfig{DatabaseName: testDB})
	res, err := s.Run(`MATCH (a:Entity{id:'org1/ns1/api'}) DETACH DELETE a`, nil)
	if err == nil {
		_, err = res.Consume()
	}
	s.Close()
	if err != nil {
		t.Fatalf("delete terminated app: %v", err)
	}

	if err := svc.BorrowFromNamespace(context.Background(), "org1/ns1", "org1/ns2/db", inherited); err != nil {
		t.Fatalf("cross-namespace termination borrow should succeed: %v", err)
	}
	db, _ := quotaStore.GetResourceQuotas("org1/ns2/db")
	if db["cpu"] != 5 {
		t.Errorf("successor db cpu = %g, want 5 (1 + inherited 4)", db["cpu"])
	}
	if out, _ := loans.Outstanding("org1/ns1", "org1/ns2/db", "cpu", false); out != 4 {
		t.Errorf("namespace loan outstanding = %g, want 4", out)
	}


	ns1, err := quotaStore.GetEffectiveAvailableResources(nil, "org1/ns1")
	if err != nil {
		t.Fatalf("effective available ns1: %v", err)
	}
	if ns1["cpu"] != 0 {
		t.Errorf("ns1 effective available cpu = %g, want 0 (lent out, not double-available)", ns1["cpu"])
	}
}

// runs a real cross namespace borrow and return through the validated store path with live Neo4j
func TestNeo4jBorrowFlowCrossNamespace(t *testing.T) {
	driver := neo4jTestDriver(t)
	defer driver.Close()
	seedInitial(t, driver)

	quotaStore := &resourceQuotaNeo4jStore{driver: driver, dbName: testDB}
	loans := NewLoanNeo4jStore(driver, testDB)
	svc := services.NewBorrowService(NewQuotaStoreAccessor(quotaStore), loans, NewTransferNeo4jStore(driver, testDB), noopDispatcher{})

	lender, borrower := "org1/ns1/api", "org1/ns2/db"

	if err := svc.Borrow(context.Background(), lender, borrower, "cpu", 1); err != nil {
		t.Fatalf("cross-namespace borrow via validated Neo4j path should succeed: %v", err)
	}
	api, _ := quotaStore.GetResourceQuotas(lender)
	db, _ := quotaStore.GetResourceQuotas(borrower)
	if api["cpu"] != 3 || db["cpu"] != 2 {
		t.Fatalf("after borrow want api=3 db=2, got api=%g db=%g", api["cpu"], db["cpu"])
	}
	if out, _ := loans.Outstanding(lender, borrower, "cpu", false); out != 1 {
		t.Errorf("loan outstanding = %g, want 1", out)
	}

	// Return it.
	if err := svc.Return(context.Background(), lender, borrower, "cpu", 1); err != nil {
		t.Fatalf("return via Neo4j: %v", err)
	}
	api, _ = quotaStore.GetResourceQuotas(lender)
	db, _ = quotaStore.GetResourceQuotas(borrower)
	if api["cpu"] != 4 || db["cpu"] != 1 {
		t.Fatalf("after return want api=4 db=1, got api=%g db=%g", api["cpu"], db["cpu"])
	}
	if out, _ := loans.Outstanding(lender, borrower, "cpu", false); out != 0 {
		t.Errorf("loan outstanding after return = %g, want 0", out)
	}
}

func TestNeo4jAddCommitsOnCallerTx(t *testing.T) {
	driver := neo4jTestDriver(t)
	defer driver.Close()
	seedGraph(t, driver) // clears the graph

	quotaStore := &resourceQuotaNeo4jStore{driver: driver, dbName: testDB}
	apps := NewAppNeo4jStore(driver, testDB, quotaStore)
	namespaces := NewNamespaceNeo4jStore(driver, testDB, quotaStore, apps)

	root := domain.NewNamespace("org9", "root", "v1", map[string]string{})
	if err := root.AddResourceQuota("cpu", 4); err != nil {
		t.Fatal(err)
	}
	if err := namespaces.Add(root, nil); err != nil {
		t.Fatalf("namespace Add: %v", err)
	}
	if q, _ := quotaStore.GetResourceQuotas(root.GetId()); q["cpu"] != 4 {
		t.Errorf("namespace cpu after Add = %g, want 4 (quotas not committed)", q["cpu"])
	}

	web := domain.NewApp(root, "web", "v1")
	if err := web.AddResourceQuota("cpu", 3); err != nil {
		t.Fatal(err)
	}
	if err := apps.Add(web); err != nil {
		t.Fatalf("app Add within budget: %v", err)
	}
	if q, _ := quotaStore.GetResourceQuotas(web.GetId()); q["cpu"] != 3 {
		t.Errorf("app cpu after Add = %g, want 3 (quotas not committed)", q["cpu"])
	}

	big := domain.NewApp(root, "big", "v1")
	if err := big.AddResourceQuota("cpu", 10); err != nil {
		t.Fatal(err)
	}
	if err := apps.Add(big); err == nil {
		t.Fatal("app Add over budget should fail")
	}
	s := driver.NewSession(neo4j.SessionConfig{DatabaseName: testDB})
	defer s.Close()
	res, err := s.Run(`MATCH (a:Entity{id: $id}) RETURN count(a)`, map[string]any{"id": big.GetId()})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := res.Single()
	if err != nil {
		t.Fatal(err)
	}
	if n := rec.Values[0].(int64); n != 0 {
		t.Errorf("rejected app left %d node(s) behind; Add was not rolled back", n)
	}
}

// moves both directions and both kinds onto the successor in one transaction, drops loans between the two apps, and refuses a missing successor without touching anything
func TestNeo4jReassignAll(t *testing.T) {
	driver := neo4jTestDriver(t)
	defer driver.Close()
	seedGraph(t, driver)
	runCypher(t, driver, `MERGE (:Entity{id:'org1/ns2/db2'})`, nil)

	loans := NewLoanNeo4jStore(driver, testDB)
	db, db2 := "org1/ns2/db", "org1/ns2/db2"
	recordLoan(t, driver, "org1/ns1/api", db, "cpu", 2, false) // db borrowed from api
	recordLoan(t, driver, "org1/ns2", db, "cpu", 1, true)      // db grew into its pool
	recordLoan(t, driver, db2, db, "cpu", 1, false)            // db borrowed from its successor

	if err := loans.ReassignAll(db, "org1/ns2/missing"); err == nil {
		t.Fatal("ReassignAll onto a missing successor should fail")
	}
	if l, _ := loans.Involving(db); len(l) != 3 {
		t.Fatalf("failed ReassignAll must not move anything, db has %d loans", len(l))
	}

	if err := loans.ReassignAll(db, db2); err != nil {
		t.Fatalf("ReassignAll: %v", err)
	}
	if l, _ := loans.Involving(db); len(l) != 0 {
		t.Errorf("db should have no loans left, got %+v", l)
	}
	if out, _ := loans.Outstanding("org1/ns1/api", db2, "cpu", false); out != 2 {
		t.Errorf("db2 should owe api 2 (structural), got %g", out)
	}
	if out, _ := loans.Outstanding("org1/ns2", db2, "cpu", true); out != 1 {
		t.Errorf("db2 should hold the elastic pool loan 1, got %g", out)
	}
	if l, _ := loans.Involving(db2); len(l) != 2 {
		t.Errorf("db2 should have exactly 2 loans (mutual loan dropped), got %+v", l)
	}
}
