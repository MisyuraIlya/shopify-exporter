package dto

type LocationNode struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	IsActive bool   `json:"isActive,omitempty"`
}

type LocationsQueryData struct {
	Locations struct {
		Nodes []LocationNode `json:"nodes,omitempty"`
	} `json:"locations"`
}

type InventoryItemNode struct {
	ID      string `json:"id,omitempty"`
	Tracked bool   `json:"tracked,omitempty"`
}

type VariantInventoryNode struct {
	ID            string             `json:"id,omitempty"`
	SKU           string             `json:"sku,omitempty"`
	InventoryItem *InventoryItemNode `json:"inventoryItem,omitempty"`
}

type VariantInventoryQueryData struct {
	ProductVariants struct {
		Nodes []VariantInventoryNode `json:"nodes,omitempty"`
	} `json:"productVariants"`
}

type InventorySetOnHandQuantitiesData struct {
	InventorySetOnHandQuantities struct {
		UserErrors []ShopifyUserError `json:"userErrors,omitempty"`
	} `json:"inventorySetOnHandQuantities"`
}

type InventoryItemUpdateData struct {
	InventoryItemUpdate struct {
		InventoryItem *InventoryItemNode `json:"inventoryItem,omitempty"`
		UserErrors    []ShopifyUserError `json:"userErrors,omitempty"`
	} `json:"inventoryItemUpdate"`
}

type InventoryActivateData struct {
	InventoryActivate struct {
		InventoryLevel *struct {
			ID string `json:"id,omitempty"`
		} `json:"inventoryLevel,omitempty"`
		UserErrors []ShopifyUserError `json:"userErrors,omitempty"`
	} `json:"inventoryActivate"`
}
