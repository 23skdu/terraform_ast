package changelog

import (
	"context"
)

// Aggregator collects changelogs across multiple versions
type Aggregator struct {
	GitHubClient interface{} // Placeholder for github client
}

func NewAggregator() *Aggregator {
	return &Aggregator{}
}

// Aggregate gathers release notes between versions
func (a *Aggregator) Aggregate(ctx context.Context, moduleSource, fromVersion, toVersion string) (string, error) {
	// TODO: Fetch release notes from upstream repository between versions
	return "Changelog aggregation not yet implemented.", nil
}
