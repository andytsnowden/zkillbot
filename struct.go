package main

import (
	"context"

	"github.com/antihax/goesi"
	"github.com/bwmarrin/discordgo"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

type ZKillBot struct {
	// Config Management
	viperConfig *viper.Viper

	// Logging
	log *logrus.Logger

	// goesi Client
	esiClient *goesi.APIClient

	// Discord Methods
	discord *discordgo.Session

	// Discord message commands
	eveIDLookup chan discordCommand

	// Context
	ctx context.Context
}

/*
	Struct for passing commands from discord to zkill functions
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
