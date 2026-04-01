package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/jobs"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
)

// handleAdminImageUploadURL generates a presigned R2 PUT URL for browser-direct
// upload. No file data passes through this server.
//
// POST /admin/images/upload-url
// Form: content_type (e.g. "image/jpeg")
// Response: { "upload_url": "...", "r2_key": "..." }
func (d *Deps) handleAdminImageUploadURL(w http.ResponseWriter, r *http.Request) {
	contentType := r.FormValue("content_type")
	if contentType == "" {
		contentType = "image/jpeg"
	}

	r2Key := fmt.Sprintf("products/%s", uuid.New().String())

	uploadURL, err := d.R2Client.PresignPutURL(r.Context(), r2Key, contentType, 15*time.Minute)
	if err != nil {
		Error(w, r, err)
		return
	}
	JSON(w, http.StatusOK, map[string]string{
		"upload_url": uploadURL,
		"r2_key":     r2Key,
	})
}

// handleAdminProductImageCreate persists a product media record after the
// browser has uploaded the image to R2.
//
// POST /admin/catalog/{id}/images
// Form: r2_key, alt_text, position
func (d *Deps) handleAdminProductImageCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	productID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseMultipartForm(32 << 20); err != nil {
		Error(w, r, err)
		return
	}

	r2Key := r.FormValue("r2_key")
	if r2Key == "" {
		Error(w, r, errors.New("r2_key is required"))
		return
	}

	altText := r.FormValue("alt_text")
	position, _ := strconv.Atoi(r.FormValue("position"))
	actor := staffActor(r)

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.CatalogService.CreateProductMedia(ctx, tx, store.CreateProductMediaParams{
			ProductID: productID,
			R2Key:     r2Key,
			AltText:   altText,
			Position:  position,
			MediaType: domain.MediaTypeImage,
		}, actor)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	d.renderMediaGallery(w, r, productID)
}

// handleAdminProductImageDelete removes a product media record and enqueues
// a River job to delete the image from R2.
//
// POST /admin/catalog/{id}/images/{imageID}/delete
func (d *Deps) handleAdminProductImageDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	productID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	imageID, err := uuid.Parse(r.PathValue("imageID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	actor := staffActor(r)

	var r2Key string
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		r2Key, txErr = d.CatalogService.DeleteProductMedia(ctx, tx, imageID, actor)
		if txErr != nil {
			return txErr
		}

		// Enqueue R2 image deletion in the same transaction.
		_, txErr = d.RiverClient.InsertTx(ctx, tx, jobs.R2ImageDeleteArgs{
			R2Key: r2Key,
		}, nil)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	d.renderMediaGallery(w, r, productID)
}

// handleAdminProductImageSetPrimary moves an image to position 0 (primary) and shifts others.
//
// POST /admin/catalog/{id}/images/{imageID}/primary
func (d *Deps) handleAdminProductImageSetPrimary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	productID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	imageID, err := uuid.Parse(r.PathValue("imageID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		media, txErr := d.CatalogService.ListProductMedia(ctx, tx, productID)
		if txErr != nil {
			return txErr
		}

		// Build new order: target image first, then the rest in existing order.
		pos := 0
		if txErr := d.CatalogService.UpdateProductMediaPosition(ctx, tx, imageID, pos); txErr != nil {
			return txErr
		}
		pos++
		for _, m := range media {
			if m.ID == imageID {
				continue
			}
			if txErr := d.CatalogService.UpdateProductMediaPosition(ctx, tx, m.ID, pos); txErr != nil {
				return txErr
			}
			pos++
		}
		return nil
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	d.renderMediaGallery(w, r, productID)
}

// handleAdminProductImageReorder updates the position of all images for a product.
//
// POST /admin/catalog/{id}/images/reorder
// Body (JSON): { "image_ids": ["uuid1", "uuid2", ...] }
// Or form: image_ids[]=uuid1&image_ids[]=uuid2
func (d *Deps) handleAdminProductImageReorder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	_, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var imageIDs []uuid.UUID
	contentType := r.Header.Get("Content-Type")
	if contentType == "application/json" {
		var body struct {
			ImageIDs []uuid.UUID `json:"image_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			Error(w, r, err)
			return
		}
		imageIDs = body.ImageIDs
	} else {
		if err := r.ParseForm(); err != nil {
			Error(w, r, err)
			return
		}
		for _, idStr := range r.Form["image_ids[]"] {
			id, parseErr := uuid.Parse(idStr)
			if parseErr != nil {
				continue
			}
			imageIDs = append(imageIDs, id)
		}
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		for i, id := range imageIDs {
			if txErr := d.CatalogService.UpdateProductMediaPosition(ctx, tx, id, i); txErr != nil {
				return txErr
			}
		}
		return nil
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// renderMediaGallery loads and renders the media gallery partial.
func (d *Deps) renderMediaGallery(w http.ResponseWriter, r *http.Request, productID uuid.UUID) {
	ctx := r.Context()

	var mediaList []domain.ProductMedia
	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		mediaList, txErr = d.CatalogService.ListProductMedia(ctx, tx, productID)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	props := admin.MediaGalleryProps{
		ProductID:   productID,
		Media:       mediaList,
		MediaConfig: d.MediaConfig,
	}
	admin.MediaGallery(props).Render(ctx, w) //nolint:errcheck
}
