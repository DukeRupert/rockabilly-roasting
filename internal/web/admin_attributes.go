package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
	"github.com/dukerupert/hiri/internal/ui/components/toast"
)

// --- Attribute Set List ---

func (d *Deps) handleAdminAttributeSetList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var sets []domain.AttributeSet
	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		sets, txErr = d.AttributeService.ListAttributeSets(ctx, tx)
		if txErr != nil {
			return txErr
		}
		// Load keys for each set
		for i := range sets {
			keys, kErr := d.AttributeService.ListAttributeKeys(ctx, tx, sets[i].ID)
			if kErr != nil {
				return kErr
			}
			sets[i].Keys = keys
		}
		return nil
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	name, role := staffNameRole(r)
	props := admin.AttributeSetListProps{
		Sets:      sets,
		Flash:     r.URL.Query().Get("flash"),
		StaffName: name,
		StaffRole: role,
	}
	if IsHTMX(r) {
		admin.AttributeSetListContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.AttributeSetList(props).Render(ctx, w) //nolint:errcheck
}

// --- Create Attribute Set ---

func (d *Deps) handleAdminAttributeSetCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	name := r.FormValue("name")
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	slug := r.FormValue("slug")
	if slug == "" {
		slug = slugify(name)
	}

	position, _ := strconv.Atoi(r.FormValue("position"))

	params := store.CreateAttributeSetParams{
		Name:     name,
		Slug:     slug,
		Position: position,
	}

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.AttributeService.CreateAttributeSet(ctx, tx, params, staffActor(r))
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/attributes?flash=Attribute+set+created", http.StatusSeeOther)
}

// --- Edit Attribute Set ---

func (d *Deps) handleAdminAttributeSetEdit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var set *domain.AttributeSet
	var keys []domain.AttributeKey

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		set, txErr = d.AttributeService.GetAttributeSet(ctx, tx, id)
		if txErr != nil {
			return txErr
		}
		keys, txErr = d.AttributeService.ListAttributeKeys(ctx, tx, id)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	name, role := staffNameRole(r)
	props := admin.AttributeSetEditProps{
		Set:       set,
		Keys:      keys,
		Flash:     r.URL.Query().Get("flash"),
		StaffName: name,
		StaffRole: role,
	}
	if IsHTMX(r) {
		admin.AttributeSetEditContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.AttributeSetEdit(props).Render(ctx, w) //nolint:errcheck
}

// --- Update Attribute Set ---

func (d *Deps) handleAdminAttributeSetUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	position, _ := strconv.Atoi(r.FormValue("position"))
	slug := r.FormValue("slug")
	if slug == "" {
		slug = slugify(r.FormValue("name"))
	}

	params := store.UpdateAttributeSetParams{
		ID:       id,
		Name:     r.FormValue("name"),
		Slug:     slug,
		Position: position,
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.AttributeService.UpdateAttributeSet(ctx, tx, params, staffActor(r))
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/attributes/%s?flash=Attribute+set+updated", id), http.StatusSeeOther)
}

// --- Delete Attribute Set ---

func (d *Deps) handleAdminAttributeSetDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.AttributeService.DeleteAttributeSet(ctx, tx, id, staffActor(r))
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/attributes?flash=Attribute+set+deleted", http.StatusSeeOther)
}

// --- Create Attribute Key ---

func (d *Deps) handleAdminAttributeKeyCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	setID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	name := r.FormValue("name")
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	slug := r.FormValue("slug")
	if slug == "" {
		slug = slugify(name)
	}

	valueType := domain.AttributeValueType(r.FormValue("value_type"))
	if !isValidValueType(valueType) {
		valueType = domain.AttributeValueTypeText
	}

	position, _ := strconv.Atoi(r.FormValue("position"))

	var allowedValues []string
	if raw := strings.TrimSpace(r.FormValue("allowed_values")); raw != "" {
		for _, v := range strings.Split(raw, ",") {
			v = strings.TrimSpace(v)
			if v != "" {
				allowedValues = append(allowedValues, v)
			}
		}
	}

	params := store.CreateAttributeKeyParams{
		AttributeSetID: setID,
		Name:           name,
		Slug:           slug,
		ValueType:      valueType,
		Position:       position,
		Filterable:     r.FormValue("filterable") == "true",
		Sortable:       r.FormValue("sortable") == "true",
		AllowedValues:  allowedValues,
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.AttributeService.CreateAttributeKey(ctx, tx, params)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/attributes/%s?flash=Key+added", setID), http.StatusSeeOther)
}

// --- Update Attribute Key ---

func (d *Deps) handleAdminAttributeKeyUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	setID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	keyID, err := uuid.Parse(r.PathValue("keyID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	slug := r.FormValue("slug")
	if slug == "" {
		slug = slugify(r.FormValue("name"))
	}

	valueType := domain.AttributeValueType(r.FormValue("value_type"))
	if !isValidValueType(valueType) {
		valueType = domain.AttributeValueTypeText
	}

	position, _ := strconv.Atoi(r.FormValue("position"))

	var allowedValues []string
	if raw := strings.TrimSpace(r.FormValue("allowed_values")); raw != "" {
		for _, v := range strings.Split(raw, ",") {
			v = strings.TrimSpace(v)
			if v != "" {
				allowedValues = append(allowedValues, v)
			}
		}
	}

	params := store.UpdateAttributeKeyParams{
		ID:            keyID,
		Name:          r.FormValue("name"),
		Slug:          slug,
		ValueType:     valueType,
		Position:      position,
		Filterable:    r.FormValue("filterable") == "true",
		Sortable:      r.FormValue("sortable") == "true",
		AllowedValues: allowedValues,
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.AttributeService.UpdateAttributeKey(ctx, tx, params)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/attributes/%s?flash=Key+updated", setID), http.StatusSeeOther)
}

// --- Delete Attribute Key ---

func (d *Deps) handleAdminAttributeKeyDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	setID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	keyID, err := uuid.Parse(r.PathValue("keyID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.AttributeService.DeleteAttributeKey(ctx, tx, keyID)
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/attributes/%s?flash=Key+deleted", setID), http.StatusSeeOther)
}

// --- Product Attribute Assignment ---

func (d *Deps) handleAdminProductAttributeAssign(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	productID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	setID, err := uuid.Parse(r.FormValue("attribute_set_id"))
	if err != nil {
		http.Error(w, "invalid attribute set", http.StatusBadRequest)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.AttributeService.AssignAttributeSetToProduct(ctx, tx, productID, setID)
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	if IsHTMX(r) {
		d.renderAttributesPanel(w, r, productID)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/catalog/%s?flash=Attribute+set+assigned", productID), http.StatusSeeOther)
}

func (d *Deps) handleAdminProductAttributeRemove(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	productID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	setID, err := uuid.Parse(r.FormValue("attribute_set_id"))
	if err != nil {
		http.Error(w, "invalid attribute set", http.StatusBadRequest)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.AttributeService.RemoveAttributeSetFromProduct(ctx, tx, productID, setID)
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	if IsHTMX(r) {
		d.renderAttributesPanel(w, r, productID)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/catalog/%s?flash=Attribute+set+removed", productID), http.StatusSeeOther)
}

// --- Save Product Attribute Values ---

func (d *Deps) handleAdminProductAttributeSave(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	productID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Collect all boolean key IDs first so we can default absent checkboxes to "false".
	booleanKeys := make(map[uuid.UUID]bool)
	for key := range r.Form {
		if !strings.HasPrefix(key, "vt_") {
			continue
		}
		if r.FormValue(key) == string(domain.AttributeValueTypeBoolean) {
			keyIDStr := strings.TrimPrefix(key, "vt_")
			if kid, err := uuid.Parse(keyIDStr); err == nil {
				booleanKeys[kid] = true
			}
		}
	}

	// Parse attribute values from form by value type.
	values := make(map[uuid.UUID]store.AttributeValueInput)
	for key, formValues := range r.Form {
		if !strings.HasPrefix(key, "attr_") || len(formValues) == 0 {
			continue
		}
		keyIDStr := strings.TrimPrefix(key, "attr_")
		keyID, parseErr := uuid.Parse(keyIDStr)
		if parseErr != nil {
			continue
		}

		vtKey := "vt_" + keyIDStr
		valueType := r.FormValue(vtKey)

		switch domain.AttributeValueType(valueType) {
		case domain.AttributeValueTypeMultiText:
			raw := strings.TrimSpace(formValues[0])
			if raw == "" {
				continue
			}
			parts := strings.Split(raw, ",")
			var cleaned []string
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p != "" {
					cleaned = append(cleaned, p)
				}
			}
			if len(cleaned) > 0 {
				values[keyID] = store.AttributeValueInput{Values: cleaned}
			}
		case domain.AttributeValueTypeMultiEnum:
			// Multiple checkbox values come as separate form entries
			var cleaned []string
			for _, v := range formValues {
				v = strings.TrimSpace(v)
				if v != "" {
					cleaned = append(cleaned, v)
				}
			}
			if len(cleaned) > 0 {
				values[keyID] = store.AttributeValueInput{Values: cleaned}
			}
		case domain.AttributeValueTypeBoolean:
			// Checkbox present = "true"
			val := "true"
			values[keyID] = store.AttributeValueInput{Value: &val}
			delete(booleanKeys, keyID) // mark as handled
		default:
			// text, enum: single value
			raw := strings.TrimSpace(formValues[0])
			if raw == "" {
				continue
			}
			values[keyID] = store.AttributeValueInput{Value: &raw}
		}
	}

	// Default absent boolean checkboxes to "false"
	for kid := range booleanKeys {
		val := "false"
		values[kid] = store.AttributeValueInput{Value: &val}
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.AttributeService.SaveProductAttributes(ctx, tx, productID, values, staffActor(r))
	})
	if err != nil {
		if IsHTMX(r) {
			d.renderAttributesPanel(w, r, productID)
			_, msg := mapError(err)
			toast.Toast(toast.VariantError, msg).Render(r.Context(), w) //nolint:errcheck
			return
		}
		Error(w, r, err)
		return
	}

	if IsHTMX(r) {
		d.renderAttributesPanel(w, r, productID)
		toast.Toast(toast.VariantSuccess, "Attributes saved").Render(r.Context(), w) //nolint:errcheck
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/catalog/%s?flash=Attributes+saved", productID), http.StatusSeeOther)
}

// --- Panel helper ---

func (d *Deps) renderAttributesPanel(w http.ResponseWriter, r *http.Request, productID uuid.UUID) {
	ctx := r.Context()

	var product *domain.Product
	var assignedSets []domain.AttributeSet
	var allSets []domain.AttributeSet
	var attrValues []domain.ProductAttributeValue

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		product, txErr = d.CatalogService.GetProduct(ctx, tx, productID)
		if txErr != nil {
			return txErr
		}
		assignedSets, txErr = d.AttributeService.ListProductAttributeSets(ctx, tx, productID)
		if txErr != nil {
			return txErr
		}
		// Load keys for each assigned set
		for i := range assignedSets {
			keys, kErr := d.AttributeService.ListAttributeKeys(ctx, tx, assignedSets[i].ID)
			if kErr != nil {
				return kErr
			}
			assignedSets[i].Keys = keys
		}
		allSets, txErr = d.AttributeService.ListAttributeSets(ctx, tx)
		if txErr != nil {
			return txErr
		}
		attrValues, txErr = d.AttributeService.ListProductAttributeValues(ctx, tx, productID)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	admin.AttributesPanel(admin.AttributesPanelProps{
		Product:      product,
		AssignedSets: assignedSets,
		AllSets:      allSets,
		Values:       attrValues,
	}).Render(ctx, w) //nolint:errcheck
}

// isValidValueType checks if a value type is one of the known types.
func isValidValueType(vt domain.AttributeValueType) bool {
	switch vt {
	case domain.AttributeValueTypeText,
		domain.AttributeValueTypeEnum,
		domain.AttributeValueTypeMultiText,
		domain.AttributeValueTypeMultiEnum,
		domain.AttributeValueTypeBoolean:
		return true
	}
	return false
}
