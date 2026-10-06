package player

import (
	"context"
	"fmt"
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

// SkipTimes queries api.aniskip.com (the same service ani-cli's --skip uses).
func SkipTimes(ctx context.Context, d *db.DB, malID, episode int) []SkipInterval {
	if malID <= 0 || episode <= 0 {
		return nil
	}
	key := fmt.Sprintf("aniskip:%d:%d", malID, episode)
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
			SkipType string `json:"skipType"`
		} `json:"results"`
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	url := fmt.Sprintf("https://api.aniskip.com/v2/skip-times/%d/%d?types[]=op&types[]=ed&types[]=recap&types[]=mixed-op&types[]=mixed-ed&episodeLength=0", malID, episode)
	if err := util.GetJSON(ctx, url, nil, &resp); err != nil {
		return nil
	}
	for _, r := range resp.Results {
		out = append(out, SkipInterval{Type: r.SkipType, Start: r.Interval.StartTime, End: r.Interval.EndTime})
	}
	d.SetCache(key, out, 7*24*time.Hour)
	return out
}
