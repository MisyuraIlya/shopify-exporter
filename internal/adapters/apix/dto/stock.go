package dto

type Stock struct {
	ItemKey     string  `json:"ITEMKEY"`
	ItemWarHBal float64 `json:"ITEMWARHBAL"`
}

type StockResponse struct {
	Api    string  `json:"api"`
	Status string  `json:"status"`
	Items  []Stock `json:"items"`
}
