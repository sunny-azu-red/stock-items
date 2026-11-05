// Package stock holds the domain model and all pure business rules
// (units, validation, grouping, exports, kit readiness and service due
// dates). It has no database or HTTP dependencies.
package stock

import (
	"cmp"
	"slices"
	"strconv"
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
	Version    int // see Item.Version; odometer readings don't count
	// ArchivedAt is set for vehicles no longer kept: they leave the lists
	// and pickers, but their services and kits stay.
	ArchivedAt *time.Time
}

// Archived reports whether the vehicle has been archived.
func (v Vehicle) Archived() bool { return v.ArchivedAt != nil }

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
	SKUBarcode      SKUKind = "barcode" // EAN, UPC, or GTIN-8/12/13/14 barcode
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
		return "Barcode"
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
	Brand    string   `json:"brand"`
	PackSize *float64 `json:"pack_size"`
}

type skuSortKey struct {
	tier      int     // 0: matches item brand, 1: OEM/KTM, 2: other brand
	brand     string  // normalized brand name for grouping
	packSize  float64 // pack size ascending (0 if nil)
	isBarcode int     // 0: part number, 1: barcode
}

// SortSKUs returns a copy of skus ordered for display:
//  1. SKUs matching the current item brand appear first (or OEM/KTM if brand is KTM/OEM).
//     Then OEM/KTM SKUs appear (if brand was not KTM/OEM), followed by other brands grouped together.
//  2. Ascending pack size (e.g. 1 L before 4 L; nil/unspecified sizes come first).
//  3. Part numbers (OEM, Manufacturer, etc.) before barcodes.
//  4. Stable order for ties.
func SortSKUs(skus []SKU, itemBrand string) []SKU {
	if len(skus) <= 1 {
		return slices.Clone(skus)
	}
	brandNorm := strings.ToLower(strings.TrimSpace(itemBrand))
	isOEMBrand := brandNorm == "oem" || brandNorm == "ktm"

	key := func(s SKU) skuSortKey {
		b := strings.ToLower(strings.TrimSpace(s.Brand))
		if b == "" && s.Kind == SKUOEM {
			b = "ktm"
		}
		isOEM := s.Kind == SKUOEM || b == "ktm" || b == "oem" || strings.HasPrefix(b, "ktm")
		matches := false
		if isOEMBrand {
			matches = isOEM
		} else if brandNorm != "" {
			matches = b == brandNorm || strings.HasPrefix(b, brandNorm)
		}

		tier := 2
		if matches {
			tier = 0
		} else if isOEM {
			tier = 1
		}

		brandGroup := b
		if matches {
			brandGroup = "" // All matching brand SKUs share top group
		} else if isOEM {
			brandGroup = "ktm"
		}

		pack := 0.0
		if s.PackSize != nil {
			pack = *s.PackSize
		}

		barcode := 0
		if s.Kind == SKUBarcode {
			barcode = 1
		}

		return skuSortKey{tier: tier, brand: brandGroup, packSize: pack, isBarcode: barcode}
	}

	out := slices.Clone(skus)
	slices.SortStableFunc(out, func(a, b SKU) int {
		ka, kb := key(a), key(b)
		if c := cmp.Compare(ka.tier, kb.tier); c != 0 {
			return c
		}
		if c := cmp.Compare(ka.brand, kb.brand); c != 0 {
			return c
		}
		if c := cmp.Compare(ka.packSize, kb.packSize); c != 0 {
			return c
		}
		if c := cmp.Compare(ka.isBarcode, kb.isBarcode); c != 0 {
			return c
		}
		return 0
	})
	return out
}

// Item is a part or consumable in stock.
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
	// Version counts the changes to what the item's edit form and page
	// show; forms carry it, so one opened before a change is refused
	// (store.StaleError). Stock changes don't count: they are deltas.
	Version int
	// ArchivedAt is set for items no longer kept: they are hidden from
	// stock, pickers and suggestions, but their history stays.
	ArchivedAt *time.Time
}

// DisplaySKUs returns the item's SKUs ordered for display according to its current Brand.
func (it Item) DisplaySKUs() []SKU {
	return SortSKUs(it.SKUs, it.Brand)
}

// Archived reports whether the item has been archived.
func (it Item) Archived() bool { return it.ArchivedAt != nil }

// ItemUsage counts the records that refer to an item. An item in use is
// archived instead of deleted, so those records stay intact.
type ItemUsage struct {
	Purchases int // its purchases count in the spending figures
	Services  int // services that used it
	Kits      int // kits that list it
}

// InUse reports whether anything refers to the item.
func (u ItemUsage) InUse() bool { return u.Purchases+u.Services+u.Kits > 0 }

// String lists the references, e.g. "2 services, 1 kit and 5 purchases".
func (u ItemUsage) String() string {
	var parts []string
	add := func(n int, one, many string) {
		switch {
		case n == 1:
			parts = append(parts, "1 "+one)
		case n > 1:
			parts = append(parts, strconv.Itoa(n)+" "+many)
		}
	}
	add(u.Services, "service", "services")
	add(u.Kits, "kit", "kits")
	add(u.Purchases, "purchase", "purchases")
	if len(parts) <= 1 {
		return strings.Join(parts, "")
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
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
// An item with nothing left has nothing to expire.
func (it Item) ExpiryState(today time.Time, days int) string {
	if it.ExpiresOn == nil || it.Quantity <= 0 {
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
		return "Starting stock"
	case MovePurchase:
		return "Bought"
	case MoveUse:
		return "Used"
	case MoveCorrection:
		return "Counted"
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
	Version         int // see Item.Version
}

// Kit is a reusable list of parts for a recurring service job, with an
// optional interval used for reminders (plus an optional intermediate
// half-interval schedule and parts list).
type Kit struct {
	ID                  int64
	VehicleID           int64
	VehicleName         string
	VehicleColor        string
	VehicleOdometerKm   *int
	Name                string
	IntervalKm          *int
	IntervalMonths      *int
	InterIntervalKm     *int
	InterIntervalMonths *int
	Notes               string
	Lines               []KitLine
	InterLines          []KitLine
	LastService         *ServiceRef
	// LastOdometerKm is the reading of the latest service with this kit
	// that has one; km reminders count from it when the last service was
	// logged without a reading.
	LastOdometerKm *int
	// LastFullService is the latest full (non-intermediate) service performed
	// with this kit, and LastFullOdometerKm is the latest full reading.
	LastFullService    *ServiceRef
	LastFullOdometerKm *int
	Version            int // see Item.Version
	// ArchivedAt is set for kits no longer used: they leave lists and
	// reminders, but their services keep them.
	ArchivedAt *time.Time
}

// Archived reports whether the kit has been archived.
func (k Kit) Archived() bool { return k.ArchivedAt != nil }

// HasIntermediate reports whether the kit has an intermediate schedule or
// intermediate parts.
func (k Kit) HasIntermediate() bool {
	return k.InterIntervalKm != nil || k.InterIntervalMonths != nil || len(k.InterLines) > 0
}

// ServiceRef is the minimal information about a past service.
type ServiceRef struct {
	ID          int64
	PerformedOn time.Time
	OdometerKm  *int
	// OdometerAtLog is the vehicle's reading when a service without its
	// own reading was logged, if that was within a week of doing it.
	OdometerAtLog *int
	Intermediate  bool
}

// KitLine is one item (and needed quantity) of a kit.
type KitLine struct {
	ItemID   int64
	ItemName string
	Unit     Unit
	Need     float64
	Have     float64
	Archived bool // the item is archived; the kit should be edited
}

// Short is how much is missing to cover this line.
func (l KitLine) Short() float64 {
	if l.Have >= l.Need {
		return 0
	}
	return RoundQty(l.Need - l.Have)
}

// ServiceRecord is a performed service job.
type ServiceRecord struct {
	ID              int64
	VehicleID       int64
	VehicleName     string
	VehicleColor    string
	KitID           *int64
	KitName         string
	KitIntermediate bool
	PerformedOn     time.Time
	OdometerKm      *int
	Title           string
	Notes           string
	LaborCost       *float64
	Parts           []ServicePart
	PartCount       int
	CreatedAt       time.Time
	Version         int // see Item.Version
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
	Version   int // see Item.Version
}

// Title is the item display name or the free text.
func (e ShoppingEntry) Title() string {
	if e.ItemName != "" {
		return e.ItemName
	}
	return e.FreeText
}

// DateOnly truncates t to midnight UTC of its calendar date in t's own
// location. That is how calendar days are kept: pgx reads a date column
// as midnight UTC, and time.Now() is in time.Local, the app's zone, so
// DateOnly(time.Now()) is today there. For an instant read from the
// database, convert it to the app's zone first (t.In(loc)).
func DateOnly(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// TrashEntry is something deleted that can still be restored.
type TrashEntry struct {
	ID        int64
	Kind      string
	Label     string
	DeletedAt time.Time
}
