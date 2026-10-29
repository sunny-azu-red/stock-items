// Package stock holds the domain model and all pure business rules
// (units, validation, grouping, exports, kit readiness and service due
// dates). It has no database or HTTP dependencies.
package stock

import (
	"strings"
	"time"
)

// VehicleKind is the type of a vehicle.
type VehicleKind string

const (
	KindMotorcycle VehicleKind = "motorcycle"
	KindCar        VehicleKind = "car"
	KindOther      VehicleKind = "other"
)

// VehicleKinds lists the valid kinds in display order.
var VehicleKinds = []VehicleKind{KindMotorcycle, KindCar, KindOther}

// Label is a human readable name for the kind.
func (k VehicleKind) Label() string {
	switch k {
	case KindMotorcycle:
		return "Motorcycle"
	case KindCar:
		return "Car"
	default:
		return "Other"
	}
}

// Icon is the sprite icon name for the kind.
func (k VehicleKind) Icon() string {
	switch k {
	case KindMotorcycle:
		return "bike"
	case KindCar:
		return "car"
	default:
		return "truck"
	}
}

// Vehicle is something items are kept in stock for.
type Vehicle struct {
	ID         int64
	Name       string
	Kind       VehicleKind
	Make       string
	Model      string
	Year       *int
	Plate      string
	VIN        string
	Color      string
	OdometerKm *int
	Notes      string
	ItemCount  int
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Subtitle is "Make Model (Year)" with empty parts left out.
func (v Vehicle) Subtitle() string {
	s := strings.TrimSpace(v.Make + " " + v.Model)
	if v.Year != nil {
		if s != "" {
			s += " "
		}
		s += "(" + itoa(*v.Year) + ")"
	}
	return s
}

// SKUKind describes where a SKU / part number comes from.
type SKUKind string

const (
	SKUOEM          SKUKind = "oem"
	SKUManufacturer SKUKind = "manufacturer"
	SKURetailer     SKUKind = "retailer"
	SKUBarcode      SKUKind = "ean" // EAN / UPC / GTIN barcode number
	SKUOther        SKUKind = "other"
)

// SKUKinds lists the valid SKU kinds in display order.
var SKUKinds = []SKUKind{SKUOEM, SKUManufacturer, SKUBarcode, SKURetailer, SKUOther}

// Label is a human readable name for the SKU kind.
func (k SKUKind) Label() string {
	switch k {
	case SKUOEM:
		return "OEM"
	case SKUManufacturer:
		return "Manufacturer"
	case SKURetailer:
		return "Retailer"
	case SKUBarcode:
		return "EAN / Barcode"
	default:
		return "Other"
	}
}

// SKU is one part number of an item. An item can have many (OEM number,
// manufacturer number, one per pack size, ...).
type SKU struct {
	ID       int64    `json:"id"`
	Code     string   `json:"code"`
	Kind     SKUKind  `json:"kind"`
	Label    string   `json:"label"`
	PackSize *float64 `json:"pack_size"`
}

// Item is a part or consumable on stock.
type Item struct {
	ID          int64
	Name        string
	Brand       string
	Category    string
	Unit        Unit
	Quantity    float64
	MinQuantity *float64
	ExpiresOn   *time.Time
	Location    string
	Notes       string
	SKUs        []SKU
	VehicleIDs  []int64
	HasPhoto    bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// DisplayName is "Brand Name", unless the name already starts with the brand.
func (it Item) DisplayName() string {
	b, n := strings.TrimSpace(it.Brand), strings.TrimSpace(it.Name)
	if b == "" || strings.HasPrefix(strings.ToLower(n), strings.ToLower(b)) {
		return n
	}
	return b + " " + n
}

// IsLow reports whether the quantity is below the minimum stock level.
func (it Item) IsLow() bool {
	return it.MinQuantity != nil && it.Quantity < *it.MinQuantity
}

// IsOut reports whether there is nothing left.
func (it Item) IsOut() bool { return it.Quantity <= 0 }

// ExpiryState returns "expired", "soon" (within days), or "" for an item.
func (it Item) ExpiryState(today time.Time, days int) string {
	if it.ExpiresOn == nil {
		return ""
	}
	t := DateOnly(today)
	switch {
	case it.ExpiresOn.Before(t):
		return "expired"
	case !it.ExpiresOn.After(t.AddDate(0, 0, days)):
		return "soon"
	}
	return ""
}

// HasVehicle reports whether the item is tagged with vehicle id.
func (it Item) HasVehicle(id int64) bool {
	for _, v := range it.VehicleIDs {
		if v == id {
			return true
		}
	}
	return false
}

// MovementKind is the reason a stock quantity changed.
type MovementKind string

const (
	MoveInitial    MovementKind = "initial"
	MovePurchase   MovementKind = "purchase"
	MoveUse        MovementKind = "use"
	MoveCorrection MovementKind = "correction"
	MoveService    MovementKind = "service"
)

// Label is a human readable name for the movement kind.
func (k MovementKind) Label() string {
	switch k {
	case MoveInitial:
		return "Initial stock"
	case MovePurchase:
		return "Purchase"
	case MoveUse:
		return "Used"
	case MoveCorrection:
		return "Correction"
	case MoveService:
		return "Service"
	}
	return string(k)
}

// Movement is one entry of the stock ledger.
type Movement struct {
	ID              int64
	ItemID          int64
	ItemName        string
	ItemUnit        Unit
	Kind            MovementKind
	Delta           float64
	TotalPrice      *float64
	Supplier        string
	Note            string
	OccurredOn      time.Time
	ServiceRecordID *int64
	ServiceTitle    string
	CreatedAt       time.Time
}

// Kit is a reusable list of parts for a recurring service job, with an
// optional interval used for reminders.
type Kit struct {
	ID             int64
	VehicleID      int64
	VehicleName    string
	VehicleColor   string
	Name           string
	IntervalKm     *int
	IntervalMonths *int
	Notes          string
	Lines          []KitLine
	LastService    *ServiceRef
}

// ServiceRef is the minimal information about a past service.
type ServiceRef struct {
	ID          int64
	PerformedOn time.Time
	OdometerKm  *int
}

// KitLine is one item (and needed quantity) of a kit.
type KitLine struct {
	ItemID   int64
	ItemName string
	Unit     Unit
	Need     float64
	Have     float64
}

// Short is how much is missing to cover this line.
func (l KitLine) Short() float64 {
	if l.Have >= l.Need {
		return 0
	}
	return round3(l.Need - l.Have)
}

// ServiceRecord is a performed service job.
type ServiceRecord struct {
	ID           int64
	VehicleID    int64
	VehicleName  string
	VehicleColor string
	KitID        *int64
	KitName      string
	PerformedOn  time.Time
	OdometerKm   *int
	Title        string
	Notes        string
	LaborCost    *float64
	Parts        []ServicePart
	PartCount    int
	CreatedAt    time.Time
}

// ServicePart is an item consumed by a service.
type ServicePart struct {
	ItemID   int64
	ItemName string
	Unit     Unit
	Quantity float64
}

// ShoppingEntry is something to buy. It is either linked to an item or is
// free text.
type ShoppingEntry struct {
	ID        int64
	ItemID    *int64
	ItemName  string
	ItemUnit  Unit
	FreeText  string
	Quantity  *float64
	Note      string
	CreatedAt time.Time
}

// Title is the item display name or the free text.
func (e ShoppingEntry) Title() string {
	if e.ItemName != "" {
		return e.ItemName
	}
	return e.FreeText
}

// DateOnly truncates t to midnight UTC of its calendar date.
func DateOnly(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
