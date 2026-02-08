package dto

type ProductsOrderRequest struct {
	DbName string `json:"dbName"`
}

type ProductOrderCategoryDto struct {
	CategoryNoteID  int    `json:"categoryNoteId"`
	CategoryValue   string `json:"categoryValue"`
	CategoryEnglish string `json:"categoryEnglish"`
	OrderNoteID     int    `json:"orderNoteId"`
	OrderValue      string `json:"orderValue"`
	OrderNumber     int    `json:"orderNumber"`
}

type ProductOrderDto struct {
	Sku        string                    `json:"sku"`
	Categories []ProductOrderCategoryDto `json:"categories"`
}

type ProductsOrderResponse struct {
	Api           string            `json:"api"`
	Status        string            `json:"status"`
	ProductsCount int               `json:"productsCount"`
	Products      []ProductOrderDto `json:"products"`
}
