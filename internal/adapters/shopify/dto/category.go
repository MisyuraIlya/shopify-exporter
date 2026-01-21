package dto

type ShopifyCollection struct {
	ID     string `json:"id,omitempty"`
	Title  string `json:"title,omitempty"`
	Handle string `json:"handle,omitempty"`
}

type ShopifyCollectionConnection struct {
	Nodes    []ShopifyCollection `json:"nodes,omitempty"`
	PageInfo ShopifyPageInfo     `json:"pageInfo,omitempty"`
}

type CollectionsQueryData struct {
	Collections ShopifyCollectionConnection `json:"collections"`
}

type CollectionCreateData struct {
	CollectionCreate CollectionCreatePayload `json:"collectionCreate"`
}

type CollectionCreatePayload struct {
	Collection *ShopifyCollection `json:"collection"`
	UserErrors []ShopifyUserError `json:"userErrors,omitempty"`
}

type CollectionUpdateData struct {
	CollectionUpdate CollectionUpdatePayload `json:"collectionUpdate"`
}

type CollectionUpdatePayload struct {
	Collection *ShopifyCollection `json:"collection"`
	UserErrors []ShopifyUserError `json:"userErrors,omitempty"`
}

type CollectionAddProductsData struct {
	CollectionAddProducts CollectionAddProductsPayload `json:"collectionAddProducts"`
}

type CollectionAddProductsPayload struct {
	UserErrors []ShopifyUserError `json:"userErrors,omitempty"`
}
