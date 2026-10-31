package stock

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxMoney is the largest price or cost accepted. It keeps values inside
// the numeric(12,2) columns.
const MaxMoney = 100_000_000

// Text and number limits keep fields readable and values sane.
const (
	maxShortText = 200  // names, brands, shops, notes on a stock change…
	maxLongText  = 4000 // notes on items, vehicles, kits and services
	// MaxOdometerKm is the largest odometer reading accepted.
	MaxOdometerKm  = 10_000_000
	maxIntervalKm  = 1_000_000
	maxIntervalMon = 240
)

// limitText records an error on the form's summary when value is longer
// than max characters (most text fields have no error slot of their own).
func limitText(errs Errors, max int, label, value string) {
	if utf8.RuneCountInString(value) > max {
		errs.Add("form", fmt.Sprintf("%s is too long (max %s characters).", label, FormatInt(max)))
	}
}

// validMoney reports whether an optional amount of money is in range. NaN
// fails both comparisons, so it is rejected too.
func validMoney(p *float64) bool { return p == nil || (*p >= 0 && *p <= MaxMoney) }

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
	// QuantityWas is the quantity the edit form was opened with. When
	// Quantity still equals it, an update leaves the stock alone.
	QuantityWas *float64
	// TotalPrice, Supplier and BoughtOn describe how a new item's starting
	// stock was bought. They are only used on create; see StartMovement.
	TotalPrice *float64
	Supplier   string
	BoughtOn   *time.Time
	// Version is the item's version as the edit form was opened (see
	// Item.Version); unused on create.
	Version int
	// FormKey is the form's one-time key: a repeat of a save that went
	// through does nothing (see store.AlreadySavedError). "" for none.
	FormKey string
}

// Bought reports whether a price or shop was given for the starting stock.
func (in ItemInput) Bought() bool { return in.TotalPrice != nil || in.Supplier != "" }

// StartMovement is the movement recording a new item's starting stock: a
// purchase when a price or shop was given, otherwise "initial" stock that
// was already on the shelf.
func (in ItemInput) StartMovement(itemID int64) MovementInput {
	if !in.Bought() {
		return MovementInput{ItemID: itemID, Kind: MoveInitial, Delta: in.Quantity, OccurredOn: in.BoughtOn}
	}
	return MovementInput{
		ItemID: itemID, Kind: MovePurchase, Delta: in.Quantity,
		TotalPrice: in.TotalPrice, Supplier: in.Supplier, OccurredOn: in.BoughtOn,
	}
}

// Normalize trims text fields and drops empty SKU rows / duplicate tags.
func (in *ItemInput) Normalize() {
	in.Name = strings.TrimSpace(in.Name)
	in.Brand = strings.TrimSpace(in.Brand)
	in.Category = strings.TrimSpace(in.Category)
	in.Location = strings.TrimSpace(in.Location)
	in.Notes = strings.TrimSpace(in.Notes)
	in.Supplier = strings.TrimSpace(in.Supplier)
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
	} else if utf8.RuneCountInString(in.Name) > 200 {
		errs.Add("name", "Name is too long (max 200 characters)")
	}
	if !in.Unit.Valid() {
		errs.Add("unit", "Choose how this item is counted")
	}
	if in.Quantity < 0 {
		errs.Add("quantity", "Quantity cannot be negative")
	} else if in.Bought() && in.Quantity == 0 {
		errs.Add("quantity", "Enter how much you bought")
	}
	if !validMoney(in.TotalPrice) {
		errs.Add("total_price", "Enter a price between 0 and "+FormatInt(MaxMoney))
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
			errs.Add(field, "Not a valid barcode (8, 12, 13 or 14 digits with a correct check digit). For a part number, pick OEM or Manufacturer.")
		}
		if s.PackSize != nil && *s.PackSize <= 0 {
			errs.Add(field, "Pack size must be greater than zero")
		}
	}
	limitText(errs, maxShortText, "Brand", in.Brand)
	limitText(errs, maxShortText, "Category", in.Category)
	limitText(errs, maxShortText, "Location", in.Location)
	limitText(errs, maxShortText, "Shop", in.Supplier)
	limitText(errs, maxLongText, "Notes", in.Notes)
	for i, sku := range in.SKUs {
		if utf8.RuneCountInString(sku.Code) > maxShortText || utf8.RuneCountInString(sku.Label) > maxShortText {
			errs.Add(fmt.Sprintf("sku_%d", i), "Code and label can have at most 200 characters")
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
	// KeepOdometer says the edit form's odometer was left as it was opened,
	// so an update keeps the current reading (it may have changed since).
	KeepOdometer bool
	Version      int    // as the edit form was opened; unused on create
	FormKey      string // see ItemInput.FormKey
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
	} else if utf8.RuneCountInString(in.Name) > 80 {
		errs.Add("name", "Name is too long (max 80 characters)")
	}
	if in.Kind != KindMotorcycle && in.Kind != KindCar && in.Kind != KindOther {
		errs.Add("kind", "Unknown vehicle type")
	}
	if in.Year != nil && (*in.Year < 1900 || *in.Year > 2100) {
		errs.Add("year", "Year must be between 1900 and 2100")
	}
	if !colorRe.MatchString(in.Color) {
		errs.Add("color", "Colour must look like #ff6600")
	}
	if in.OdometerKm != nil && *in.OdometerKm < 0 {
		errs.Add("odometer_km", "Odometer cannot be negative")
	}
	if in.OdometerKm != nil && *in.OdometerKm > MaxOdometerKm {
		errs.Add("odometer_km", "That is more than "+FormatKm(MaxOdometerKm))
	}
	limitText(errs, maxShortText, "Make", in.Make)
	limitText(errs, maxShortText, "Model", in.Model)
	limitText(errs, maxShortText, "Plate", in.Plate)
	limitText(errs, maxShortText, "VIN", in.VIN)
	limitText(errs, maxLongText, "Notes", in.Notes)
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
	// ExpiresOn is the best-before date of a purchased batch. When set, it
	// replaces the item's expiry date.
	ExpiresOn *time.Time
	FormKey   string // see ItemInput.FormKey
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
	if !validMoney(in.TotalPrice) {
		errs.Add("total_price", "Enter a price between 0 and "+FormatInt(MaxMoney))
	} else if (in.TotalPrice != nil || in.Supplier != "") && in.Kind != MovePurchase {
		// Spending reads only purchases, so a price anywhere else would be
		// stored but never counted.
		errs.Add("total_price", "Only a purchase has a price or shop")
	}
	if in.ExpiresOn != nil && in.Kind != MovePurchase {
		errs.Add("expires_on", "Only a purchase can set the best-before date")
	}
	limitText(errs, maxShortText, "Shop", in.Supplier)
	limitText(errs, maxShortText, "Note", in.Note)
	return errs
}

// Editable reports whether a stock change can be edited or deleted on
// its own. Parts used by a service are changed through the service; one
// left without its service (service_record_id is ON DELETE SET NULL) can
// still be fixed on its own.
func (m Movement) Editable() bool { return m.Kind != MoveService || m.ServiceRecordID == nil }

// TakesPrice reports whether a stock change can carry a price and shop.
func (m Movement) TakesPrice() bool { return m.Kind == MovePurchase || m.Kind == MoveInitial }

// MovementEdit is the corrected data for a recorded stock change. Amount
// is the size of the change; its sign follows the kind, except for a count
// (a correction or quick -/+), which can go either way: Removed says it took
// stock away. A nil OccurredOn keeps the date.
type MovementEdit struct {
	Amount     float64
	Removed    bool
	TotalPrice *float64
	Supplier   string
	Note       string
	OccurredOn *time.Time
	Version    int    // the movement's version as the form was opened
	FormKey    string // see ItemInput.FormKey
}

// ApplyEdit returns m corrected by e. A kind without a price and shop
// (a use, a count) is saved without them. Initial stock that gets a price
// or shop becomes a purchase, as when an item is created.
func ApplyEdit(m Movement, e MovementEdit) (MovementInput, Errors) {
	errs := Errors{}
	if !m.Editable() {
		errs.Add("form", "Parts used by a service are changed by editing the service")
		return MovementInput{}, errs
	}
	in := MovementInput{
		ItemID: m.ItemID, Kind: m.Kind, Delta: m.Delta, TotalPrice: m.TotalPrice, Supplier: m.Supplier,
		Note: strings.TrimSpace(e.Note), OccurredOn: &m.OccurredOn, ServiceRecordID: m.ServiceRecordID,
	}
	if e.OccurredOn != nil {
		in.OccurredOn = e.OccurredOn
	}
	if e.Amount <= 0 {
		errs.Add("amount", "Amount must be greater than zero")
	}
	in.Delta = e.Amount
	if m.Kind == MoveUse || m.Kind == MoveService || (m.Kind == MoveCorrection && e.Removed) {
		in.Delta = -e.Amount
	}
	if m.TakesPrice() {
		in.TotalPrice, in.Supplier = e.TotalPrice, strings.TrimSpace(e.Supplier)
		if in.Kind == MoveInitial && (in.TotalPrice != nil || in.Supplier != "") {
			in.Kind = MovePurchase
		}
	} else {
		in.TotalPrice, in.Supplier = nil, "" // not shown on the form
	}
	for k, v := range in.Validate() {
		errs.Add(k, v)
	}
	return in, errs
}

// earliestEventDate is the oldest date accepted for a purchase, stock
// change or service. Anything earlier is almost certainly a typo.
var earliestEventDate = time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC)

// CheckEventDate returns a message when d is not a plausible date for
// something that already happened: before 1990 or after tomorrow (one day
// of slack for time zones). A nil date is fine; it means today.
func CheckEventDate(d *time.Time, today time.Time) string {
	if d == nil {
		return ""
	}
	if d.Before(earliestEventDate) {
		return "Date is too far in the past"
	}
	if d.After(DateOnly(today).AddDate(0, 0, 1)) {
		return "Date cannot be in the future"
	}
	return ""
}

// KitInput is the data needed to create or update a service kit.
type KitInput struct {
	VehicleID      int64
	Name           string
	IntervalKm     *int
	IntervalMonths *int
	Notes          string
	Lines          []KitLineInput
	Version        int    // as the edit form was opened; unused on create
	FormKey        string // see ItemInput.FormKey
}

// KitLineInput is one item of a kit.
type KitLineInput struct {
	ItemID   int64
	Quantity float64
	// Unit is the item's unit the quantity was read in, so a save can
	// refuse a line whose item changed unit meanwhile ("" skips the check).
	Unit Unit
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
		switch {
		case l.Quantity <= 0:
			errs.Add(fmt.Sprintf("line_%d", i), "Quantity must be greater than zero")
		case l.Quantity > MaxQuantity:
			// Rows of the same item are added up by Normalize, so the form's
			// rows can each be fine.
			errs.Add("lines", "Together that is more than "+FormatInt(MaxQuantity)+" of one item")
		}
	}
	if utf8.RuneCountInString(in.Name) > maxShortText {
		errs.Add("name", "Name is too long (max 200 characters)")
	}
	if in.IntervalKm != nil && *in.IntervalKm > maxIntervalKm {
		errs.Add("interval_km", "That is more than "+FormatKm(maxIntervalKm))
	}
	if in.IntervalMonths != nil && *in.IntervalMonths > maxIntervalMon {
		errs.Add("interval_months", "That is more than 20 years")
	}
	limitText(errs, maxLongText, "Notes", in.Notes)
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
	Version     int    // as the edit form was opened; unused on create
	FormKey     string // see ItemInput.FormKey
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
	if !validMoney(in.LaborCost) {
		errs.Add("labor_cost", "Enter a cost between 0 and "+FormatInt(MaxMoney))
	}
	for i, l := range in.Parts {
		if l.Quantity <= 0 {
			errs.Add(fmt.Sprintf("line_%d", i), "Quantity must be greater than zero")
		}
	}
	if utf8.RuneCountInString(in.Title) > maxShortText {
		errs.Add("title", "Title is too long (max 200 characters)")
	}
	if in.OdometerKm != nil && *in.OdometerKm > MaxOdometerKm {
		errs.Add("odometer_km", "That is more than "+FormatKm(MaxOdometerKm))
	}
	limitText(errs, maxLongText, "Notes", in.Notes)
	return errs
}

// ShoppingInput adds an entry to the shopping list.
type ShoppingInput struct {
	ItemID   *int64
	FreeText string
	Quantity *float64
	Unit     Unit // of Quantity; for a linked item, the item's unit
	Note     string
	FormKey  string // see ItemInput.FormKey
}

// Normalize trims text fields and defaults the unit to pieces.
func (in *ShoppingInput) Normalize() {
	in.FreeText = strings.TrimSpace(in.FreeText)
	in.Note = strings.TrimSpace(in.Note)
	if in.Unit == "" {
		in.Unit = UnitPiece
	}
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
	if in.Unit != "" && !in.Unit.Valid() {
		errs.Add("quantity", "Unknown unit")
	}
	if utf8.RuneCountInString(in.FreeText) > maxShortText {
		errs.Add("free_text", "That is too long (max 200 characters)")
	}
	limitText(errs, maxShortText, "Note", in.Note)
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
			out[i].Quantity = RoundQty(out[i].Quantity + l.Quantity)
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
