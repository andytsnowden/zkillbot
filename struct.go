package main

import (
	"context"
	"sync"

	"github.com/antihax/goesi"
	"github.com/bwmarrin/discordgo"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

/*
	Main struct for bot
*/
type ZKillBot struct {
	// Context
	ctx context.Context

	// Mux
	mux sync.Mutex

	// Config Management
	viperConfig *viper.Viper

	// Logging
	log *logrus.Logger

	// goesi Client
	esiClient *goesi.APIClient

	// Discord websocket session
	discord *discordgo.Session

	// Channels
	eveIDLookup   chan discordCommand
	zkillMessage  chan string
	zkillTracking chan discordCommand

	// zkillboard websocket
	zKillboard *websocket.Conn

	// Subscription data structures
	dataStorage *DataStorage
}

/*
	passing commands from discord to zkill functions
*/
type discordCommand struct {
	ChannelID string
	Message   string
}

/*
	Not sure if this will be a thing yet
	Potentially store looked up values as a cache, less ESI calls
*/
type cacheData struct {
	IDtoConv []struct {
		Num12654 struct {
			Type  string `json:"Type"`
			Value string `json:"Value"`
		} `json:"12654"`
		Num45677 struct {
			Type  string `json:"Type"`
			Value string `json:"Value"`
		} `json:"45677"`
	} `json:"IDtoConv"`
}

/*
   Storage for mappings between channels and eve along with settings for each
*/
type DataStorage struct {
	// Discord Channel -> Eve ID
	ChannelMap map[string]map[int]*subscriptionData
	// Eve ID -> Discord Channel
	SubMap map[int]map[string]*subscriptionData
}
type subscriptionData struct {
	DiscordChannelID string `json:"discord_channel_id"`
	EveID            int    `json:"eve_id"`
	EveName          string `json:"eve_name"`
	EveCategory      string `json:"eve_category"`
	MinVal           int    `json:"min_val"`
}

/*
   Kill Summary
*/
type KillSummary struct {
	Action        string `json:"action"`
	KillID        int    `json:"killID"`
	CharacterID   int    `json:"character_id"`
	CorporationID int    `json:"corporation_id"`
	AllianceID    int    `json:"alliance_id"`
	ShipTypeID    int    `json:"ship_type_id"`
	URL           string `json:"url"`
}
