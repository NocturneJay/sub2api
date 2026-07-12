package dto

import "testing"

func TestParseCustomMenuItemsPreservesOpenMode(t *testing.T) {
	items := ParseCustomMenuItems(`[{"id":"image2","label":"Image2","icon_svg":"","url":"https://image2.example.com","open_mode":"new_tab","visibility":"user","sort_order":0}]`)
	if len(items) != 1 {
		t.Fatalf("ParseCustomMenuItems() returned %d items, want 1", len(items))
	}
	if items[0].OpenMode != "new_tab" {
		t.Fatalf("OpenMode = %q, want new_tab", items[0].OpenMode)
	}
}
