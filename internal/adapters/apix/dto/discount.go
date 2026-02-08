package dto

type Discount struct {
	ID                  int     `json:"ID"`
	AccountKey          string  `json:"AccountKey"`
	AccountDiscountCode int     `json:"AccountDiscountCode"`
	ItemDiscountCode    string  `json:"ItemDiscountCode"`
	PriceListNumber     int     `json:"PriceListNumber"`
	DiscountPrc         float64 `json:"DiscountPrc"`
	CommisionPrc        float64 `json:"CommisionPrc"`
}

type DiscountResponse struct {
	Api           string     `json:"api"`
	Status        string     `json:"status"`
	DiscountCount int        `json:"discountsCount"`
	Discounts     []Discount `json:"discounts"`
}
