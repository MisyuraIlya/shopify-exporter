package dto

import "time"

type ProductDto struct {
	ID                 int       `json:"ID"`
	ItemKey            string    `json:"ItemKey"`
	ItemName           string    `json:"ItemName"`
	ForignName         string    `json:"ForignName"`
	Filter             string    `json:"Filter"`
	SortGroup          int       `json:"SortGroup"`
	SalesUnit          string    `json:"SalesUnit"`
	Price              float64   `json:"Price"`
	DiscountCode       string    `json:"DiscountCode"`
	VatExampt          int       `json:"VatExampt"`
	SuF4               int       `json:"SuF4"`
	Localization       string    `json:"LOCALIZATION"`
	BarCode            string    `json:"BarCode"`
	Dumi               int       `json:"Dumi"`
	StockPerUnit       int       `json:"StockPerUnit"`
	DiscountPrc        float64   `json:"DiscountPrc"`
	ExPic              string    `json:"ExPic"`
	Weight             float64   `json:"Weight"`
	PurchPrice         float64   `json:"PurchPrice"`
	Note               string    `json:"Note"`
	NoteName           string    `json:"NoteName"`
	Orden              int64     `json:"orden"`
	Purchased          float64   `json:"purchased"`
	WebItem            int       `json:"webItem"`
	PackQuantity       float64   `json:"packQuantity"`
	ExpectedReturnDate time.Time `json:"expectedReturnDate"`
	Status             bool      `json:"status"`
}

type ProductResponse struct {
	Api           string       `json:"api"`
	Status        string       `json:"status"`
	CurrentPage   int          `json:"currentPage"`
	PageSize      int          `json:"pageSize"`
	TotalPages    int          `json:"totalPages"`
	ProductsCount int          `json:"productsCount"`
	Products      []ProductDto `json:"products"`
}
