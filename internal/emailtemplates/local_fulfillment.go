package emailtemplates

// OrderReadyForPickupData holds data for the "your order is ready" email
// sent when a pickup order is marked ready for collection.
type OrderReadyForPickupData struct {
	CustomerName       string
	OrderNumber        string
	PickupInstructions string // address + hours, configured in admin settings
	StoreName          string
	StoreURL           string
	AccountURL         string
}

// OrderOutForDeliveryData holds data for the "out for local delivery today"
// email sent when staff dispatches the local-delivery route.
type OrderOutForDeliveryData struct {
	CustomerName string
	OrderNumber  string
	DeliveryDays string // configured display string ("Mondays and Thursdays")
	ShippingAddr string
	StoreName    string
	StoreURL     string
	AccountURL   string
}
