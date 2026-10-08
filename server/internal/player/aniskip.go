package player

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/simo1337s/animetest/server/internal/db"
	"github.com/simo1337s/animetest/server/internal/util"
)

// SkipInterval is an opening/ending/recap range reported by AniSkip.
type SkipInterval struct {
	Type  string  `json:"type"` // op | ed | recap | mixed-op | mixed-ed
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// aniskipAPI is AniSkip's address (tests change it).
var aniskipAPI = "https://api.aniskip.com"

// SkipTimes queries api.aniskip.com (the same service ani-cli's --skip uses)
// for an episode that is length seconds long. AniSkip's times are recorded
// on one version of an episode (TV, Blu-ray, a dub, a site's cut), and land
// on random scenes in a version that's longer or shorter: like AniSkip's
// own player extension, only times recorded on a version of about the same
// length count (AniSkip picks them, ±20s), shifted by the difference.
// Without the length there's nothing to go by, so no times.
func SkipTimes(ctx context.Context, d *db.DB, malID, episode int, length float64) []SkipInterval {
	if malID <= 0 || episode <= 0 || length <= 0 || math.IsInf(length, 0) {
		return nil
	}
	secs := int(math.Round(length))
	key := fmt.Sprintf("aniskip:%d:%d:%d", malID, episode, secs)
	var out []SkipInterval
	if d.GetCache(key, &out) {
		return out
	}
	var resp struct {
		Found   bool `json:"found"`
		Results []struct {
			Interval struct {
				StartTime float64 `json:"startTime"`
				EndTime   float64 `json:"endTime"`
			} `json:"interval"`
			SkipType      string  `json:"skipType"`
			EpisodeLength float64 `json:"episodeLength"`
		} `json:"results"`
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	url := fmt.Sprintf("%s/v2/skip-times/%d/%d?types[]=op&types[]=ed&types[]=recap&types[]=mixed-op&types[]=mixed-ed&episodeLength=%d", aniskipAPI, malID, episode, secs)
	if err := util.GetJSON(ctx, url, nil, &resp); err != nil {
		return nil
	}
	types := map[string]bool{}
	for _, r := range resp.Results {
		types[r.SkipType] = true
	}
	for _, r := range resp.Results {
		// One opening and one ending: with both kinds, the extension keeps
		// the mixed one (the opening over scenes of the episode).
		if r.SkipType == "op" && types["mixed-op"] || r.SkipType == "ed" && types["mixed-ed"] {
			continue
		}
		offset := 0.0
		if r.EpisodeLength > 0 {
			offset = length - r.EpisodeLength
		}
		start, end := max(0, r.Interval.StartTime+offset), min(length, r.Interval.EndTime+offset)
		if end-start < 1 {
			continue
		}
		out = append(out, SkipInterval{Type: r.SkipType, Start: start, End: end})
	}
	// Times for a new episode come within days of it airing.
	ttl := 7 * 24 * time.Hour
	if len(out) == 0 {
		ttl = 6 * time.Hour
	}
	d.SetCache(key, out, ttl)
	return out
}
