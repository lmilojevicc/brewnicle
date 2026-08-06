package catalog

type formulaJSON struct {
	Name        string   `json:"name"`
	Description string   `json:"desc"`
	Homepage    string   `json:"homepage"`
	OldNames    []string `json:"oldnames"`
	Disabled    bool     `json:"disabled"`
}

type caskJSON struct {
	Token       string   `json:"token"`
	Description string   `json:"desc"`
	Homepage    string   `json:"homepage"`
	OldTokens   []string `json:"old_tokens"`
	Disabled    bool     `json:"disabled"`
}
