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

// InventoryQuantityNode is one named quantity bucket (on_hand, available, …).
type InventoryQuantityNode struct {
	Name     string `json:"name,omitempty"`
	Quantity int    `json:"quantity"`
}

// InventoryLevelNode is an inventory item's state at one location.
type InventoryLevelNode struct {
	ID         string                  `json:"id,omitempty"`
	Quantities []InventoryQuantityNode `json:"quantities,omitempty"`
}

// OnHand returns the on_hand quantity and whether Shopify reported one. A brand new
// item has no level at the location yet, which is not the same as a level of zero.
func (n *InventoryLevelNode) OnHand() (int, bool) {
	if n == nil {
		return 0, false
	}
	for _, quantity := range n.Quantities {
		if quantity.Name == "on_hand" {
			return quantity.Quantity, true
		}
	}
	return 0, false
}

type InventoryItemNode struct {
	ID      string `json:"id,omitempty"`
	Tracked bool   `json:"tracked,omitempty"`
	// InventoryLevel is populated only when the query asks for a specific location.
	InventoryLevel *InventoryLevelNode `json:"inventoryLevel,omitempty"`
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
