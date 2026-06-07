package contract

type ExtractionResult struct {
	Memories []ExtractedMemory `json:"memories"`
	Entities []ExtractedEntity `json:"entities"`
	Edges    []ExtractedEdge   `json:"edges"`
}

type ExtractedMemory struct {
	Type       string  `json:"type"`
	Slot       string  `json:"slot"`
	EntityKey  string  `json:"entity_key"`
	Value      string  `json:"value"`
	Confidence float64 `json:"confidence"`
	Evidence   string  `json:"evidence"`
	Mutation   string  `json:"mutation"`
	ValidFrom  *string `json:"valid_from"`
}

type ExtractedEntity struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type ExtractedEdge struct {
	Source    string  `json:"source"`
	Relation  string  `json:"relation"`
	Target    string  `json:"target"`
	ValidFrom *string `json:"valid_from"`
}
