package domain

import "sort"

// The equipment catalogue: the machines a roaster is most likely to be putting
// on the register, offered as type-ahead suggestions on the add-machine form.
//
// This is a convenience, never a constraint. Make and Model stay free text —
// every suggestion here is a hint the crew can ignore, and a machine nobody
// anticipated is typed in exactly as it always was. Nothing validates against
// this list, so a name falling out of it can never reject a real machine.
//
// It exists because the register is only worth having if its rows agree with
// each other. Typed fresh each time, "La Marzocco", "LaMarzocco" and
// "la marzocco" become three different makes, and the question this module was
// built to answer — what have we replaced on this account, how often — stops
// being answerable by anything but reading every row.
//
// It lives in Go rather than a table because it is reference data that changes
// about as often as the product line does, and a table would need a screen, a
// migration and a release to earn its keep. If the crew start wanting to edit
// it without a deploy, that is the signal to promote it — not before.

// EquipmentCatalogEntry is one machine the shop is likely to service.
type EquipmentCatalogEntry struct {
	Make  string
	Model string
	// Category is what the machine is, so a future picker can set it from the
	// same list. The type-ahead does not read it today.
	Category EquipmentCategory
}

// equipmentCatalog is the curated list, grouped by category for readability.
//
// DRAFTED, NOT AUTHORITATIVE. This is a starting point covering equipment
// common in cafes, not a record of what this shop actually services. Cut what
// you do not touch and add what is missing — a list padded with machines nobody
// here has ever seen is worse than a short accurate one, because it makes the
// suggestions worth ignoring.
var equipmentCatalog = []EquipmentCatalogEntry{
	// --- Espresso machines ---
	{"La Marzocco", "Linea PB", EquipmentCategoryEspressoMachine},
	{"La Marzocco", "Linea Mini", EquipmentCategoryEspressoMachine},
	{"La Marzocco", "Linea Classic S", EquipmentCategoryEspressoMachine},
	{"La Marzocco", "GB5", EquipmentCategoryEspressoMachine},
	{"La Marzocco", "KB90", EquipmentCategoryEspressoMachine},
	{"La Marzocco", "Strada", EquipmentCategoryEspressoMachine},
	{"La Marzocco", "Leva", EquipmentCategoryEspressoMachine},
	{"Nuova Simonelli", "Appia Life", EquipmentCategoryEspressoMachine},
	{"Nuova Simonelli", "Aurelia Wave", EquipmentCategoryEspressoMachine},
	{"Nuova Simonelli", "Musica", EquipmentCategoryEspressoMachine},
	{"Victoria Arduino", "White Eagle", EquipmentCategoryEspressoMachine},
	{"Victoria Arduino", "Black Eagle", EquipmentCategoryEspressoMachine},
	{"Victoria Arduino", "Eagle One", EquipmentCategoryEspressoMachine},
	{"Slayer", "Espresso", EquipmentCategoryEspressoMachine},
	{"Slayer", "Steam LP", EquipmentCategoryEspressoMachine},
	{"Synesso", "MVP Hydra", EquipmentCategoryEspressoMachine},
	{"Synesso", "S200", EquipmentCategoryEspressoMachine},
	{"Rancilio", "Classe 5", EquipmentCategoryEspressoMachine},
	{"Rancilio", "Classe 7", EquipmentCategoryEspressoMachine},
	{"Rancilio", "Classe 11", EquipmentCategoryEspressoMachine},
	{"La Cimbali", "M26", EquipmentCategoryEspressoMachine},
	{"La Cimbali", "M39", EquipmentCategoryEspressoMachine},
	{"Sanremo", "Cafe Racer", EquipmentCategoryEspressoMachine},
	{"Astoria", "Storm", EquipmentCategoryEspressoMachine},
	{"Faema", "E71", EquipmentCategoryEspressoMachine},

	// --- Grinders ---
	{"Mahlkonig", "EK43", EquipmentCategoryGrinder},
	{"Mahlkonig", "E65S", EquipmentCategoryGrinder},
	{"Mahlkonig", "E80 Supreme", EquipmentCategoryGrinder},
	{"Mahlkonig", "K30", EquipmentCategoryGrinder},
	{"Mahlkonig", "X54", EquipmentCategoryGrinder},
	{"Mazzer", "Robur S", EquipmentCategoryGrinder},
	{"Mazzer", "Major V", EquipmentCategoryGrinder},
	{"Mazzer", "Mini", EquipmentCategoryGrinder},
	{"Mazzer", "Kony S", EquipmentCategoryGrinder},
	{"Nuova Simonelli", "Mythos One", EquipmentCategoryGrinder},
	{"Victoria Arduino", "Mythos MY75", EquipmentCategoryGrinder},
	{"Ditting", "807", EquipmentCategoryGrinder},
	{"Ditting", "KR1203", EquipmentCategoryGrinder},
	{"Fiorenzato", "F64", EquipmentCategoryGrinder},
	{"Fiorenzato", "F83", EquipmentCategoryGrinder},
	{"Compak", "E8", EquipmentCategoryGrinder},
	{"Anfim", "SP-II", EquipmentCategoryGrinder},
	{"Baratza", "Forte AP", EquipmentCategoryGrinder},

	// --- Brewers ---
	{"Fetco", "CBS-2131XTS", EquipmentCategoryBrewer},
	{"Fetco", "CBS-2141XL", EquipmentCategoryBrewer},
	{"Fetco", "CBS-52H", EquipmentCategoryBrewer},
	{"Bunn", "ICB", EquipmentCategoryBrewer},
	{"Bunn", "Axiom", EquipmentCategoryBrewer},
	{"Bunn", "VPR", EquipmentCategoryBrewer},
	{"Curtis", "G4 ThermoPro", EquipmentCategoryBrewer},
	{"Curtis", "Gemini", EquipmentCategoryBrewer},
	{"Curtis", "Seraphim", EquipmentCategoryBrewer},
	{"Marco", "Jet 6", EquipmentCategoryBrewer},
	{"Marco", "SP9", EquipmentCategoryBrewer},

	// --- Water treatment ---
	{"Everpure", "Claris", EquipmentCategoryWater},
	{"Everpure", "QC7I", EquipmentCategoryWater},
	{"Everpure", "MC2", EquipmentCategoryWater},
	{"3M", "HF35", EquipmentCategoryWater},
	{"3M", "SGLP2-CL", EquipmentCategoryWater},
	{"Pentair", "BevGuard", EquipmentCategoryWater},
	{"BWT", "bestmax", EquipmentCategoryWater},
	{"Optipure", "BWS100", EquipmentCategoryWater},
}

// EquipmentCatalog returns the curated entries in list order.
func EquipmentCatalog() []EquipmentCatalogEntry {
	out := make([]EquipmentCatalogEntry, len(equipmentCatalog))
	copy(out, equipmentCatalog)
	return out
}

// EquipmentMakes returns every distinct make, alphabetically.
//
// Sorted rather than left in catalogue order because a browser renders a
// datalist in the order given, and a suggestion list that is not alphabetical
// reads as random to somebody scanning it.
func EquipmentMakes() []string {
	return distinctSorted(func(e EquipmentCatalogEntry) string { return e.Make })
}

// EquipmentModels returns every distinct model, alphabetically.
//
// Deliberately flat rather than narrowed to the make already typed. A datalist
// filters on what the user types, so "Linea" surfaces only the Linea models
// without any code doing the narrowing — and a make the crew typed themselves,
// which is not in this list at all, still gets a useful model list rather than
// an empty one.
func EquipmentModels() []string {
	return distinctSorted(func(e EquipmentCatalogEntry) string { return e.Model })
}

// distinctSorted pulls one field off every catalogue entry, dropping blanks and
// duplicates, and sorts what is left.
func distinctSorted(field func(EquipmentCatalogEntry) string) []string {
	seen := make(map[string]bool, len(equipmentCatalog))
	out := make([]string, 0, len(equipmentCatalog))
	for _, e := range equipmentCatalog {
		v := field(e)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
