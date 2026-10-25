package store

import (
	"strings"
	"testing"
)

func TestRegionNameRanks(t *testing.T) {
	ranks := regionNameRanks([]string{"wor", "eu", "us", "ss"})
	// "ss" names no region, so it is skipped without shifting ranks.
	if ranks["World"] != 0 || ranks["Europe"] != 1 || ranks["USA"] != 2 {
		t.Errorf("ranks = %v", ranks)
	}
	if _, ok := ranks["Japan"]; ok {
		t.Error("Japan should be unranked under this priority")
	}
	// Two names sharing a shortcode rank equally.
	ranks2 := regionNameRanks([]string{"nl"})
	if ranks2["Holland"] != 0 || ranks2["Netherlands"] != 0 {
		t.Errorf("shared-shortcode ranks = %v", ranks2)
	}
}

func TestGroupJoin(t *testing.T) {
	// Off: no join emitted.
	f := RomFilter{}
	if sql, args := f.groupJoin(); sql != "" || args != nil {
		t.Errorf("grouping off should emit nothing, got %q %v", sql, args)
	}

	// On with a user and platform filter: the join carries the user id (for the
	// is_main_sibling tiebreak) followed by the filter's args, and partitions
	// by the metadata-id coalesce.
	f = RomFilter{GroupByMetaID: true, UserID: 7, PlatformIDs: []int64{28}, RegionPriority: []string{"wor", "eu"}}
	sql, args := f.groupJoin()
	if !strings.Contains(sql, "ROW_NUMBER() OVER (PARTITION BY COALESCE(") {
		t.Errorf("missing window partition: %s", sql)
	}
	if !strings.Contains(sql, "CONCAT('igdb-'") || !strings.Contains(sql, "CONCAT('romm-'") {
		t.Errorf("partition key missing source cases: %s", sql)
	}
	if !strings.Contains(sql, "ru.user_id = ?") {
		t.Errorf("expected rom_user join for is_main_sibling: %s", sql)
	}
	if !strings.Contains(sql, "q.rn = 1") {
		t.Errorf("expected representative filter: %s", sql)
	}
	// args: user id first, then platform id from the subquery WHERE.
	if len(args) != 2 || args[0] != int64(7) || args[1] != int64(28) {
		t.Errorf("args = %v, want [7 28]", args)
	}

	// Without a user, the is_main_sibling key and its arg are dropped.
	f2 := RomFilter{GroupByMetaID: true, PlatformIDs: []int64{3}}
	sql2, args2 := f2.groupJoin()
	if strings.Contains(sql2, "ru.user_id") {
		t.Errorf("no user join expected: %s", sql2)
	}
	if len(args2) != 1 || args2[0] != int64(3) {
		t.Errorf("args2 = %v, want [3]", args2)
	}
}
