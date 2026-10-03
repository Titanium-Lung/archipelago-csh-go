package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

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

func (s *SlotInfo) UnmarshalJSON(data []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if len(raw) < 4 {
		return fmt.Errorf("slot_info entry: expected 4 elements, got %d", len(raw))
	}

	if err := json.Unmarshal(raw[0], &s.SlotName); err != nil {
		return fmt.Errorf("slot_info name: %w", err)
	}
	if err := json.Unmarshal(raw[1], &s.Game); err != nil {
		return fmt.Errorf("slot_info game: %w", err)
	}
	return nil
}

func newIdsToNames() *IdsToNames {
	return &IdsToNames{
		IdToItemName:     make(map[int]string),
		IdToLocationName: make(map[int]string),
	}
}

type ArchipelagoSaveFile struct {
	ClientActivityTimers []ClientActivityTimer  `json:"client_activity_timers"`
	LocationChecks       map[PlayerTuple][]int  `json:"location_checks"`
	ClientGameState      map[PlayerTuple]int    `json:"client_game_state"`
	Hints                map[PlayerTuple][]Hint `json:"hints"`
}

type ClientActivityTimer struct {
	PlayerTuple PlayerTuple
	Timer       float64
}

type PlayerTuple struct {
	Team int
	Slot int
}

type Hint struct {
	ReceivingPlayer int
	FindingPlayer   int
	Location        string
	Item            string
	Found           bool
	Entrance        string
	ItemFlags       int
	Status          int
}

func (c *ClientActivityTimer) UnmarshalJSON(data []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if len(raw) != 2 {
		return fmt.Errorf("client_activity_timer: expected 2 elements, got %d", len(raw))
	}

	var playerTuple []int
	if err := json.Unmarshal(raw[0], &playerTuple); err != nil {
		return err
	}
	if len(playerTuple) != 2 {
		return fmt.Errorf("client_activity_timer: expected [team, slot], got %v", playerTuple)
	}
	c.PlayerTuple.Team = playerTuple[0]
	c.PlayerTuple.Slot = playerTuple[1]

	if err := json.Unmarshal(raw[1], &c.Timer); err != nil {
		return fmt.Errorf("client_activity_timer timestamp: %w", err)
	}

	return nil
}

func (p *PlayerTuple) UnmarshalText(text []byte) error {
	parts := strings.Split(string(text), ",")
	if len(parts) != 2 {
		return fmt.Errorf("Expected 2 elements, got %d", len(parts))
	}
	team, err := strconv.Atoi(parts[0])
	if err != nil {
		return err
	}
	slot, err := strconv.Atoi(parts[1])
	if err != nil {
		return err
	}
	p.Team = team
	p.Slot = slot

	return nil
}

func (h *Hint) UnmarshalJSON(data []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if len(raw) != 8 {
		return fmt.Errorf("hint: expected 8 elements, got %d", len(raw))
	}

	if err := json.Unmarshal(raw[0], &h.ReceivingPlayer); err != nil {
		return err
	}
	if err := json.Unmarshal(raw[1], &h.FindingPlayer); err != nil {
		return err
	}
	if err := json.Unmarshal(raw[2], &h.Location); err != nil {
		return err
	}
	if err := json.Unmarshal(raw[3], &h.Item); err != nil {
		return err
	}
	if err := json.Unmarshal(raw[4], &h.Found); err != nil {
		return err
	}
	if err := json.Unmarshal(raw[5], &h.Entrance); err != nil {
		return err
	}
	if err := json.Unmarshal(raw[6], &h.ItemFlags); err != nil {
		return err
	}
	if err := json.Unmarshal(raw[7], &h.Status); err != nil {
		return err
	}

	return nil
}
