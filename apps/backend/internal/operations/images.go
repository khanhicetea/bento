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

const (
	KindImagePrune   = "image.prune"
	KindImagePrepare = "image.prepare"
)

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
	if err := store.DeleteRuntimeBuildsByImage(context.WithoutCancel(ctx), c.Store.DB(), img.ID); err != nil {
		return nil, err
	}
	r.Info(ctx, "removed image %s (%s)", r.Op.TargetID, strings.Join(img.Tags, ", "))
	return map[string]any{"image": r.Op.TargetID, "tags": img.Tags}, nil
}

// handleImagePrepare builds the current build of one runtime key (the target
// id, in ImageKey.String form) ahead of any app rollout. It touches no app:
// running instances keep serving on their current image, and the reconciler
// replaces them only once this has succeeded.
func (c *Controller) handleImagePrepare(ctx context.Context, r *Run) (any, error) {
	key, err := parseRuntimeKey(r.Op.TargetID)
	if err != nil {
		return nil, err
	}
	spec, err := runtime.PlanImage(key)
	if err != nil {
		return nil, err
	}
	if err := r.Phase(ctx, "build-image"); err != nil {
		return nil, err
	}
	b, err := c.ensureCurrentBuild(ctx, spec, func(s string) { r.Info(ctx, "%s", s) })
	if err != nil {
		return nil, Fail("image", "Check Docker connectivity and build output; running apps keep their current image.",
			"managed image: %v", err)
	}
	return map[string]any{"key": key.String(), "tag": b.Tag, "image": b.ImageID}, nil
}
