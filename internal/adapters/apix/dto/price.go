package dto

type PriceDto struct {
	ID              int     `json:"ID"`
	ItemKey         string  `json:"ItemKey"`
	Price           float32 `json:"Price"`
	CurrencyCode    string  `json:"CurrencyCode"`
	PriceListNumber int     `json:"PriceListNumber"`
	DatF            string  `json:"DatF"`
	DFlag           int     `json:"DFlag"`
	UseFID          int     `json:"UseFID"`
	CngDate         string  `json:"CngDate"`
}

type PriceRespone struct {
	Api         string     `json:"api"`
	Status      string     `json:"status"`
	PricesCount int        `json:"pricesCount"`
	Prices      []PriceDto `json:"prices"`
}
