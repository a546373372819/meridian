package domain


type Loan struct {
	From     string  // app|namepsace|org
	To       string 
	Resource string  // cpu, mem, disk
	Amount   float64
	Elastic bool // true - loan the stress controller created, such loans are auto reclaimed when the borrower goes idle
}

type LoanStore interface {
  
  
	Outstanding(from, to, resource string, elastic bool) (float64, error)

	BorrowedBy(app, resource string) (float64, error)
  
	List() ([]Loan, error)

	Involving(id string) ([]Loan, error)
  
	ReassignAll(oldApp, newApp string) error
}
