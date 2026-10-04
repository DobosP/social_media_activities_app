package commands

import (
	"context"
	"encoding/json"

	"github.com/DobosP/social_media_activities_app/services/server/internal/jobs"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func (s *Service) resolveCovers(ctx context.Context, input map[string]json.RawMessage) (any, error) {
	var in struct {
		Limit   int
		City    string
		DryRun  bool `json:"dry_run"`
		Recheck bool
	}
	if err := options(input, &in); err != nil {
		return nil, err
	}
	return s.Runner.ResolveCoversWithOptions(ctx, jobs.CoverResolveOptions{City: in.City, Limit: in.Limit, DryRun: in.DryRun, Recheck: in.Recheck})
}
func (s *Service) syncRoeduEvents(ctx context.Context, input map[string]json.RawMessage) (any, error) {
	var in struct {
		City          string
		Limit         int
		AppPack       string   `json:"app_pack"`
		MinConfidence *float64 `json:"min_confidence"`
		DryRun        bool     `json:"dry_run"`
		AllowRollback bool     `json:"allow_snapshot_rollback"`
		UpdatedSince  string   `json:"updated_since"`
	}
	if err := options(input, &in); err != nil {
		return nil, err
	}
	if s.Runner.Config.Roedu == nil {
		return nil, missingDependency("canonical RO-EDU client")
	}
	if in.AppPack != "" && in.AppPack != jobs.SocialPack || in.UpdatedSince != "" {
		return nil, platform.ErrInvalid
	}
	if in.City == "" {
		in.City = s.Runner.Config.RoeduCity
	}
	pack, err := s.Runner.Config.Roedu.Read(ctx, in.City)
	if err != nil {
		return nil, err
	}
	if in.Limit < 0 {
		return nil, platform.ErrInvalid
	}
	if in.Limit > 0 && in.Limit < len(pack.Items) {
		pack.Items = pack.Items[:in.Limit]
		pack.Complete = false
		pack.Mode = "partial"
	}
	confidence := 1.0
	if in.MinConfidence != nil {
		confidence = *in.MinConfidence
	}
	return s.Runner.ApplyRoeduWithOptions(ctx, pack, in.City, jobs.RoeduApplyOptions{MinConfidence: confidence, AllowSnapshotRollback: in.AllowRollback, DryRun: in.DryRun})
}
