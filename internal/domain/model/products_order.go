package model

type ProductOrderCategory struct {
	CategoryNoteID int
	CategoryValue  string
	OrderNoteID    int
	OrderValue     string
	OrderNumber    int
}

type ProductOrder struct {
	Sku        string
	Categories []ProductOrderCategory
}
