package forms

import "context"

// FollowUpsOf is FollowUps taking at most n records: two of them run at
// once are two of the api's replicas.
func (s *Service) FollowUpsOf(ctx context.Context, n int) error { return s.followUps(ctx, n) }

// AfterCaps runs f once a follow-up's caps are read.
func (s *Service) AfterCaps(f func()) { s.afterCaps = f }
