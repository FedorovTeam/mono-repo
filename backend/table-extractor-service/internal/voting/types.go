package voting

// ColumnKey is the internal stable identifier of a voting column.
type ColumnKey string

const (
	ColPresent  ColumnKey = "present"
	ColZa       ColumnKey = "za"
	ColProtiv   ColumnKey = "protiv"
	ColVozderzh ColumnKey = "vozderzh"
	ColNoVote   ColumnKey = "no_vote"
)

// Column describes a voting column in PDF coordinates (points).
// XCenterPt is an approximate X coordinate (in PDF points) of the center of the column.
type Column struct {
	Key       ColumnKey
	Title     string
	XCenterPt float64
}

// Row corresponds to one deputy's line.
type Row struct {
	Number int
	Name   string
	Marks  map[ColumnKey]bool
}

// Document is the extracted representation that can be rendered to Markdown.
type Document struct {
	HeaderText string
	Columns    []Column
	Rows       []Row
}
