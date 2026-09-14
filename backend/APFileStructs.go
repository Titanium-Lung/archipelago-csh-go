package main

type ArchipelagoFile struct {
	SlotData          map[string]any              `json:"slot_data,omitempty"` // not currently used
	SlotInfo          map[string]SlotInfo         `json:"slot_info"`
	ConnectNames      map[string][]int            `json:"connect_names"`
	Locations         map[string]map[string][]int `json:"locations"`          // key is slot number
	PrecollectedItems map[string][]int            `json:"precollected_items"` // key is slot number
	PrecollectedHints map[string]any              `json:"precollected_hints"`
	Spheres           []map[string][]int          `json:"spheres"`     // list of spheres
	Datapackage       map[string]NamesToIds       `json:"datapackage"` // key is game name
}

type SlotInfo struct {
	SlotName string
	Game     string
}

type NamesToIds struct {
	ItemNameToId     map[string]int `json:"item_name_to_id"`
	LocationNameToId map[string]int `json:"location_name_to_id"`
}

type IdsToNames struct {
	IdToItemName     map[int]string `json:"id_to_item_name"`
	IdToLocationName map[int]string `json:"id_to_location_name"`
}

func newIdsToNames() *IdsToNames {
	return &IdsToNames{
		IdToItemName:     make(map[int]string),
		IdToLocationName: make(map[int]string),
	}
}
