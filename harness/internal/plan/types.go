package plan

type Plan struct {
	MigrationType string     `json:"migration_type"`
	SourceStack   string     `json:"source_stack"`
	TargetStack   string     `json:"target_stack"`
	Items         []PlanItem `json:"items"`
}

type PlanItem struct {
	N      int    `json:"n"`
	Path   string `json:"path"`
	Action string `json:"action"`
	Risk   string `json:"risk"`
	Notes  string `json:"notes"`
	Layer  string `json:"layer"`
}
