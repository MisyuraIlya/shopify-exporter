package api

type ProductDto struct {
	ItemKey    string  `json:"ItemKey"`
	ItemName   string  `json:"ItemName"`
	ForignName string  `json:"ForignName"`
	Price      float32 `json:"Price"`
}
