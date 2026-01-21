package dto

type CategoryRequest struct {
	DbName string `json:"dbName"`
}

type CategoryDto struct {
	CategoryHewbrew string `json:"NoteHebrew,omitempty"`
	CategoryEnglish string `json:"NoteEnglish,omitempty"`
}

type ProductCategoryDto struct {
	Sku        string        `json:"kef,omitempty"`
	Categories []CategoryDto `json:"categories"`
}

type CagtegoryResponse struct {
	Api       string               `json:"api"`
	Status    string               `json:"status"`
	TotalKeFs int                  `json:"totalKeFs"`
	Results   []ProductCategoryDto `json:"results"`
}
