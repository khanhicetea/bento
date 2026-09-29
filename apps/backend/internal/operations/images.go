package operations

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

const KindImagePrune = "image.prune"

var imageDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)

// PruneImage removes one unused Bento-built image. It requires the literal
// confirmation "delete"; eligibility is re-verified when the operation runs.
func (c *Controller) PruneImage(ctx context.Context, digest, confirm, idem string) (store.Operation, error) {
	if !imageDigest.MatchString(digest) {
		return store.Operation{}, fmt.Errorf("%w: no image with that id", store.ErrNotFound)
	}
	if confirm != "delete" {
		return store.Operation{}, fmt.Errorf("%w: type exactly \"delete\" to remove this image", ErrConfirmation)
	}
	op, _, err := c.Submit(
		ctx,
		Submission{Kind: KindImagePrune, TargetKind: "image", TargetID: "sha256:" + digest, IdempotencyKey: idem},
	)
	return op, err
}

func (c *Controller) handleImagePrune(ctx context.Context, r *Run) (any, error) {
	if err := r.Phase(ctx, "remove-image"); err != nil {
		return nil, err
	}
	img, err := c.Images.Remove(ctx, r.Op.TargetID)
	if errors.Is(err, runtime.ErrImageNotPrunable) {
		if img.ID == "" {
			return map[string]any{"alreadyRemoved": true}, nil
		}
		return nil, Fail(
			"image-in-use",
			"Only unused images built by Bento can be pruned.",
			"image %s is not Bento-built or is used by a container",
			r.Op.TargetID,
		)
	}
	if err != nil {
		return nil, err
	}
	r.Info(ctx, "removed image %s (%s)", r.Op.TargetID, strings.Join(img.Tags, ", "))
	return map[string]any{"image": r.Op.TargetID, "tags": img.Tags}, nil
}
