package dbkit

// Direction represents a sort order for a query (ascending or descending)
type Direction int

const (
	// Ascending specifies ascending sort order ("order by <col> asc")
	Ascending Direction = iota
	// Descending specifies descending sort order ("order by <col> desc")
	Descending
)

type orderColumn struct {
	column    string
	direction Direction
}
