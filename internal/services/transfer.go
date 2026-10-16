package services

// applies a borrow or return as one atomic write
type TransferStore interface {
	ApplyBorrow(from, to, resource string, fromVal, toVal, amount float64, elastic bool) error

	ApplyReturn(from, to, resource string, fromVal, toVal, amount float64, elastic bool) error

	// loan from a namespace 
	ApplyNamespaceBorrow(fromNamespace, to, resource string, toVal, amount float64) error

	ApplyNamespaceReturn(fromNamespace, to, resource string, toVal, amount float64, elastic bool) error

	// elastic loan from a free pool
	ApplyPoolBorrow(fromEntity, to, resource string, toVal, amount float64) error

	// re points a loan from oldLender to newLender as an elastic loan, no quota change
	ApplyLoanRepoint(oldLender, newLender, borrower, resource string, amount float64, oldElastic bool) error
}
