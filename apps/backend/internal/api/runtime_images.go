package api

import (
	"net/http"
	"slices"

	"github.com/khanhicetea/bento/apps/backend/internal/api/dto"
	"github.com/khanhicetea/bento/apps/backend/internal/operations"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

func runtimeBuildDTO(b store.RuntimeBuild) dto.RuntimeBuild {
	m := b.Manifest
	if m == nil {
		m = map[string]string{}
	}
	return dto.RuntimeBuild{ID: b.ID, Tag: b.Tag, ImageID: b.ImageID, BuiltAt: platform.FormatTime(b.BuiltAt),
		Current: b.Current, Manifest: m}
}

func (s *Server) handleRuntimeImages(w http.ResponseWriter, r *http.Request) {
	views, err := s.C.RuntimeImages(r.Context())
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	out := dto.RuntimeImageList{Images: []dto.RuntimeImage{}}
	for _, v := range views {
		img := dto.RuntimeImage{Key: v.Key.String(), Kind: dto.RuntimeKind(v.Key.Kind), Toolchain: v.Key.Toolchain,
			Version: v.Key.Version, Builds: []dto.RuntimeBuild{}, Apps: []dto.RuntimeImageApp{}}
		if v.Current != nil {
			cur := runtimeBuildDTO(*v.Current)
			img.Current = &cur
		}
		var usable []int64
		for _, b := range v.Builds {
			img.Builds = append(img.Builds, runtimeBuildDTO(b))
			usable = append(usable, b.ID)
		}
		for _, a := range v.Apps {
			ra := dto.RuntimeImageApp{ID: a.App.ID, Slug: a.App.Slug, DesiredRuntime: dto.DesiredRuntime(a.App.DesiredRuntime),
				BuildID: a.BuildID, Behind: v.Behind(a), Rolling: a.Rolling, Changes: nonNil(a.Changes)}
			if a.PreviousBuildID != 0 && slices.Contains(usable, a.PreviousBuildID) {
				ra.RollbackBuildID = a.PreviousBuildID
			}
			img.Apps = append(img.Apps, ra)
		}
		out.Images = append(out.Images, img)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleRuntimeRebuild(w http.ResponseWriter, r *http.Request) {
	var req struct{}
	if err := decode(w, r, &req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	idem, err := idempotencyKey(r)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	op, err := s.C.RebuildImage(r.Context(), r.PathValue("key"), idem)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	s.accepted(w, op, nil)
}

func (s *Server) handleRuntimeRollout(w http.ResponseWriter, r *http.Request) {
	var req dto.RolloutRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	idem, err := idempotencyKey(r)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	op, err := s.C.RolloutImage(r.Context(), r.PathValue("key"),
		operations.RolloutRequest{BuildID: req.BuildID, AppIDs: req.AppIDs}, idem)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	s.accepted(w, op, nil)
}
