package dto

type RellatedDto struct {
	Sku         string   `json:"sku"`
	SimilarSkus []string `json:"similarSkus"`
}

type RellatedDtoResponse struct {
	Api           string        `json:"api"`
	Status        string        `json:"status"`
	ProductsCount int           `json:"productsCount"`
	Products      []RellatedDto `json:"products"`
}
