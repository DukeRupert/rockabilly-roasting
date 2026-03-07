package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/jobs"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
)

// handleAdminImageUploadURL generates a one-time direct upload URL from
// Cloudflare Images. The browser uploads directly to CF — no file data
// passes through this server.
//
// POST /admin/images/upload-url
// Response: { "upload_url": "...", "image_id": "..." }
func (d *Deps) handleAdminImageUploadURL(w http.ResponseWriter, r *http.Request) {
	result, err := d.CFImagesClient.UploadURL(r.Context())
	if err != nil {
		Error(w, r, err)
		return
	}
	JSON(w, http.StatusOK, map[string]string{
		"upload_url": result.UploadURL,
		"image_id":   result.ImageID,
	})
}

// handleAdminProductImageCreate persists a product media record after the
// browser has uploaded the image to Cloudflare Images.
//
// POST /admin/catalog/{id}/images
// Form: cf_image_id, alt_text, position
func (d *Deps) handleAdminProductImageCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	productID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseForm(); err != nil {
		Error(w, r, err)
		return
	}

	cfImageID := r.FormValue("cf_image_id")
	if cfImageID == "" {
		Error(w, r, errors.New("cf_image_id is required"))
		return
	}

	altText := r.FormValue("alt_text")
	position, _ := strconv.Atoi(r.FormValue("position"))
	actor := staffActor(r)

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.CatalogService.CreateProductMedia(ctx, tx, store.CreateProductMediaParams{
			ProductID: productID,
			CFImageID: cfImageID,
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
// a River job to delete the image from Cloudflare Images.
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

	var cfImageID string
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		cfImageID, txErr = d.CatalogService.DeleteProductMedia(ctx, tx, imageID, actor)
		if txErr != nil {
			return txErr
		}

		// Enqueue CF image deletion in the same transaction.
		_, txErr = d.RiverClient.InsertTx(ctx, tx, jobs.CFImageDeleteArgs{
			CFImageID: cfImageID,
		}, nil)
		return txErr
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
