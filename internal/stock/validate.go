package stock

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Errors maps form field names to a validation message.
type Errors map[string]string

// Add records msg for field unless the field already has an error.
func (e Errors) Add(field, msg string) {
	if _, ok := e[field]; !ok {
		e[field] = msg
	}
}

// Any reports whether there are errors.
func (e Errors) Any() bool { return len(e) > 0 }

// Error implements error so Errors can be returned directly.
func (e Errors) Error() string {
	parts := make([]string, 0, len(e))
	for k, v := range e {
		parts = append(parts, k+": "+v)
	}
	return "validation failed: " + strings.Join(parts, "; ")
}

// ErrOrNil returns e as an error when it has entries, nil otherwise.
func (e Errors) ErrOrNil() error {
	if e.Any() {
		return e
	}
	return nil
}

// ItemInput is the data needed to create or update an item.
type ItemInput struct {
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
}

// Normalize trims text fields and drops empty SKU rows / duplicate tags.
func (in *ItemInput) Normalize() {
	in.Name = strings.TrimSpace(in.Name)
	in.Brand = strings.TrimSpace(in.Brand)
	in.Category = strings.TrimSpace(in.Category)
	in.Location = strings.TrimSpace(in.Location)
	in.Notes = strings.TrimSpace(in.Notes)
	skus := in.SKUs[:0]
	for _, s := range in.SKUs {
		s.Code = strings.TrimSpace(s.Code)
		s.Label = strings.TrimSpace(s.Label)
		if s.Code == "" && s.Label == "" && s.PackSize == nil {
			continue
		}
		if s.Kind == "" {
			s.Kind = SKUOEM
		}
		if s.Kind == SKUBarcode {
			// Barcodes are often printed in groups: "7 611197 108900".
			s.Code = strings.Join(strings.Fields(s.Code), "")
		}
		skus = append(skus, s)
	}
	in.SKUs = skus
	in.VehicleIDs = uniqueIDs(in.VehicleIDs)
}

// Validate checks the input. Call Normalize first.
func (in ItemInput) Validate() Errors {
	errs := Errors{}
	if in.Name == "" {
		errs.Add("name", "Name is required")
	} else if len(in.Name) > 200 {
		errs.Add("name", "Name is too long (max 200 characters)")
	}
	if !in.Unit.Valid() {
		errs.Add("unit", "Choose how this item is counted")
	}
	if in.Quantity < 0 {
		errs.Add("quantity", "Quantity cannot be negative")
	}
	if in.MinQuantity != nil && *in.MinQuantity < 0 {
		errs.Add("min_quantity", "Minimum cannot be negative")
	}
	seen := map[string]bool{}
	for i, s := range in.SKUs {
		field := fmt.Sprintf("sku_%d", i)
		switch {
		case s.Code == "":
			errs.Add(field, "SKU code is required")
		case seen[strings.ToLower(s.Code)]:
			errs.Add(field, "Duplicate SKU code")
		}
		seen[strings.ToLower(s.Code)] = true
		if !validSKUKind(s.Kind) {
			errs.Add(field, "Unknown SKU type")
		} else if s.Kind == SKUBarcode && s.Code != "" && !ValidGTIN(s.Code) {
			errs.Add(field, "Not a valid barcode (8, 12, 13 or 14 digits with a correct check digit)")
		}
		if s.PackSize != nil && *s.PackSize <= 0 {
			errs.Add(field, "Pack size must be greater than zero")
		}
	}
	return errs
}

func validSKUKind(k SKUKind) bool {
	for _, v := range SKUKinds {
		if v == k {
			return true
		}
	}
	return false
}

// ValidGTIN reports whether code is a GTIN-8, -12 (UPC-A), -13 (EAN-13) or
// -14 with a correct GS1 check digit.
func ValidGTIN(code string) bool {
	switch len(code) {
	case 8, 12, 13, 14:
	default:
		return false
	}
	sum := 0
	for i := len(code) - 1; i >= 0; i-- {
		c := code[i]
		if c < '0' || c > '9' {
			return false
		}
		d := int(c - '0')
		// Weights alternate 1, 3, 1, ... from the right, check digit included.
		if (len(code)-1-i)%2 == 1 {
			d *= 3
		}
		sum += d
	}
	return sum%10 == 0
}

var colorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// VehicleInput is the data needed to create or update a vehicle.
type VehicleInput struct {
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
}

// Normalize trims text fields and applies defaults.
func (in *VehicleInput) Normalize() {
	in.Name = strings.TrimSpace(in.Name)
	in.Make = strings.TrimSpace(in.Make)
	in.Model = strings.TrimSpace(in.Model)
	in.Plate = strings.TrimSpace(in.Plate)
	in.VIN = strings.ToUpper(strings.TrimSpace(in.VIN))
	in.Notes = strings.TrimSpace(in.Notes)
	in.Color = strings.ToLower(strings.TrimSpace(in.Color))
	if in.Color == "" {
		in.Color = "#f97316"
	}
	if in.Kind == "" {
		in.Kind = KindMotorcycle
	}
}

// Validate checks the input. Call Normalize first.
func (in VehicleInput) Validate() Errors {
	errs := Errors{}
	if in.Name == "" {
		errs.Add("name", "Name is required")
	} else if len(in.Name) > 80 {
		errs.Add("name", "Name is too long (max 80 characters)")
	}
	if in.Kind != KindMotorcycle && in.Kind != KindCar && in.Kind != KindOther {
		errs.Add("kind", "Unknown vehicle type")
	}
	if in.Year != nil && (*in.Year < 1900 || *in.Year > 2100) {
		errs.Add("year", "Year must be between 1900 and 2100")
	}
	if !colorRe.MatchString(in.Color) {
		errs.Add("color", "Color must look like #ff6600")
	}
	if in.OdometerKm != nil && *in.OdometerKm < 0 {
		errs.Add("odometer_km", "Odometer cannot be negative")
	}
	return errs
}

// MovementInput records a stock change.
type MovementInput struct {
	ItemID          int64
	Kind            MovementKind
	Delta           float64
	TotalPrice      *float64
	Supplier        string
	Note            string
	OccurredOn      *time.Time
	ServiceRecordID *int64
}

// Validate checks the input.
func (in MovementInput) Validate() Errors {
	errs := Errors{}
	if in.Delta == 0 {
		errs.Add("amount", "Amount must not be zero")
	}
	switch in.Kind {
	case MovePurchase, MoveInitial:
		if in.Delta < 0 {
			errs.Add("amount", "A purchase must add stock")
		}
	case MoveUse, MoveService:
		if in.Delta > 0 {
			errs.Add("amount", "Using stock must remove stock")
		}
	case MoveCorrection:
	default:
		errs.Add("kind", "Unknown movement type")
	}
	if in.TotalPrice != nil && *in.TotalPrice < 0 {
		errs.Add("total_price", "Price cannot be negative")
	}
	return errs
}

// KitInput is the data needed to create or update a service kit.
type KitInput struct {
	VehicleID      int64
	Name           string
	IntervalKm     *int
	IntervalMonths *int
	Notes          string
	Lines          []KitLineInput
}

// KitLineInput is one item of a kit.
type KitLineInput struct {
	ItemID   int64
	Quantity float64
}

// Normalize trims fields and merges duplicate lines.
func (in *KitInput) Normalize() {
	in.Name = strings.TrimSpace(in.Name)
	in.Notes = strings.TrimSpace(in.Notes)
	in.Lines = mergeLines(in.Lines)
}

// Validate checks the input. Call Normalize first.
func (in KitInput) Validate() Errors {
	errs := Errors{}
	if in.VehicleID == 0 {
		errs.Add("vehicle_id", "Choose a vehicle")
	}
	if in.Name == "" {
		errs.Add("name", "Name is required")
	}
	if in.IntervalKm != nil && *in.IntervalKm <= 0 {
		errs.Add("interval_km", "Interval must be greater than zero")
	}
	if in.IntervalMonths != nil && *in.IntervalMonths <= 0 {
		errs.Add("interval_months", "Interval must be greater than zero")
	}
	for i, l := range in.Lines {
		if l.Quantity <= 0 {
			errs.Add(fmt.Sprintf("line_%d", i), "Quantity must be greater than zero")
		}
	}
	return errs
}

// ServiceInput is the data needed to log a performed service.
type ServiceInput struct {
	VehicleID   int64
	KitID       *int64
	PerformedOn time.Time
	OdometerKm  *int
	Title       string
	Notes       string
	LaborCost   *float64
	Parts       []KitLineInput
}

// Normalize trims fields and merges duplicate parts.
func (in *ServiceInput) Normalize() {
	in.Title = strings.TrimSpace(in.Title)
	in.Notes = strings.TrimSpace(in.Notes)
	in.Parts = mergeLines(in.Parts)
}

// Validate checks the input. Call Normalize first.
func (in ServiceInput) Validate() Errors {
	errs := Errors{}
	if in.VehicleID == 0 {
		errs.Add("vehicle_id", "Choose a vehicle")
	}
	if in.Title == "" {
		errs.Add("title", "Title is required")
	}
	if in.PerformedOn.IsZero() {
		errs.Add("performed_on", "Date is required")
	}
	if in.OdometerKm != nil && *in.OdometerKm < 0 {
		errs.Add("odometer_km", "Odometer cannot be negative")
	}
	if in.LaborCost != nil && *in.LaborCost < 0 {
		errs.Add("labor_cost", "Cost cannot be negative")
	}
	for i, l := range in.Parts {
		if l.Quantity <= 0 {
			errs.Add(fmt.Sprintf("line_%d", i), "Quantity must be greater than zero")
		}
	}
	return errs
}

// ShoppingInput adds an entry to the shopping list.
type ShoppingInput struct {
	ItemID   *int64
	FreeText string
	Quantity *float64
	Note     string
}

// Normalize trims text fields.
func (in *ShoppingInput) Normalize() {
	in.FreeText = strings.TrimSpace(in.FreeText)
	in.Note = strings.TrimSpace(in.Note)
}

// Validate checks the input. Call Normalize first.
func (in ShoppingInput) Validate() Errors {
	errs := Errors{}
	if in.ItemID == nil && in.FreeText == "" {
		errs.Add("free_text", "Pick an item or describe what to buy")
	}
	if in.Quantity != nil && *in.Quantity <= 0 {
		errs.Add("quantity", "Quantity must be greater than zero")
	}
	return errs
}

func mergeLines(lines []KitLineInput) []KitLineInput {
	var out []KitLineInput
	idx := map[int64]int{}
	for _, l := range lines {
		if l.ItemID == 0 {
			continue
		}
		if i, ok := idx[l.ItemID]; ok {
			out[i].Quantity = round3(out[i].Quantity + l.Quantity)
			continue
		}
		idx[l.ItemID] = len(out)
		out = append(out, l)
	}
	return out
}

func uniqueIDs(ids []int64) []int64 {
	seen := map[int64]bool{}
	out := ids[:0]
	for _, id := range ids {
		if id == 0 || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
