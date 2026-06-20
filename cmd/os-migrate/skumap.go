package main

import "strings"

// SKU translation: OrderSpace and Hiri use different SKU schemes for the same
// catalog. OrderSpace uses numeric product codes (0001–0010) with size/grind
// suffixes; Hiri uses mnemonic prefixes (C9, 2ST, …) with its own size/grind
// vocabulary. translateSKU produces a candidate Hiri SKU; the caller confirms it
// exists in the catalog (an unknown candidate is treated as "no match" and the
// line is skipped — the original OrderSpace SKU is preserved in line metadata).

// productCodeMap maps OrderSpace numeric product codes to Hiri SKU prefixes.
// 0005 White Coffee was rebranded to "Rev It Up" (RIU). 0009 (retail bags) and
// 0010 (Bunker white-label) are handled separately.
var productCodeMap = map[string]string{
	"0001": "ETH", // Ethiopia
	"0002": "CT",  // Chop Top
	"0003": "C9",  // Cloud 9
	"0004": "GUA", // Guatemala Tikal
	"0005": "RIU", // White Coffee -> Rev It Up
	"0006": "CAS", // Cascadia Decaf
	"0007": "2ST", // 2 Stroke
	"0008": "BB",  // Bike Blend
}

// retailBagMap maps OrderSpace "Retail 12oz Bags" (0009) flavor suffixes to Hiri
// product prefixes. These are 12oz single bags; we map them to whole-bean.
var retailBagMap = map[string]string{
	"C9":    "C9",  // Cloud 9
	"2STRK": "2ST", // 2 Stroke
	"GT":    "GUA", // Guatemalan Tikal
	"BB":    "BB",  // Bike Blend
	"ETH":   "ETH", // Ethiopia
	"CT":    "CT",  // Chop Top
	"CD":    "CAS", // Cascadia Decaf
}

// grindMap maps OrderSpace grind codes to Hiri grind codes. Hiri also has FP
// (French press), which has no OrderSpace equivalent.
var grindMap = map[string]string{
	"WB": "WB",  // whole bean
	"DG": "DRI", // drip grind
	"EG": "ESP", // espresso grind
}

// sizeMap maps OrderSpace sizes to Hiri sizes. The 1lb bag was retired in favor
// of a 12oz bag, so OrderSpace 1LB maps to Hiri 12O (Rev It Up is the exception,
// handled in translateSKU).
var sizeMap = map[string]string{
	"1LB": "12O",
	"3LB": "3LB",
	"5LB": "5LB",
}

// translateSKU converts an OrderSpace order-line SKU to the equivalent Hiri
// variant SKU. ok is false for SKUs with no Hiri equivalent (e.g. Bunker
// white-label) or unrecognized codes.
func translateSKU(osSKU string) (hiriSKU string, ok bool) {
	parts := strings.Split(strings.TrimSpace(osSKU), "-")
	if len(parts) < 2 {
		return "", false
	}
	code := parts[0]

	switch code {
	case "0009": // Retail 12oz Bags — single-token flavor suffix
		hiriCode, found := retailBagMap[parts[1]]
		if !found {
			return "", false
		}
		return hiriCode + "-12O-WB", true
	case "0010": // Bunker white-label — no Hiri equivalent
		return "", false
	}

	// Standard coffee: <code>-<size>-<grind>
	if len(parts) != 3 {
		return "", false
	}
	hiriCode, found := productCodeMap[code]
	if !found {
		return "", false
	}
	size, grind := parts[1], parts[2]

	// White Coffee -> Rev It Up kept a 1lb bag, but only in espresso. Other 1lb
	// grinds have no 1lb RIU variant and fall through to the 12oz mapping.
	if code == "0005" && size == "1LB" && grind == "EG" {
		return "RIU-1LB-ESP", true
	}

	hiriSize, found := sizeMap[size]
	if !found {
		return "", false
	}
	hiriGrind, found := grindMap[grind]
	if !found {
		return "", false
	}
	return hiriCode + "-" + hiriSize + "-" + hiriGrind, true
}

// isSuccessfulOrder reports whether an OrderSpace order status represents a
// completed transaction worth migrating. Cancelled and incomplete ("new")
// orders are left behind.
func isSuccessfulOrder(status string) bool {
	switch status {
	case "fulfilled", "part_fulfilled", "invoiced", "released":
		return true
	default:
		return false
	}
}

// addrKey is a normalized key for deduplicating addresses (line1 + postal code).
func addrKey(line1, postal string) string {
	norm := func(s string) string {
		return strings.ToLower(strings.Join(strings.Fields(s), " "))
	}
	return norm(line1) + "|" + norm(postal)
}
