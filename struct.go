package main

import (
	"github.com/bwmarrin/discordgo"
	"github.com/parnurzeal/gorequest"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

type ZKillBot struct {
	// Config Management
	viperConfig *viper.Viper

	// Logging
	log *logrus.Logger

	// GoRequest
	request *gorequest.SuperAgent

	// Discord Methods
	discord *discordgo.Session

	// Discord message commands
	eveIDLookup chan discordCommand
}

/*
	Struct for passing commands from discord to zkill functions
*/
type discordCommand struct {
	ChannelID string
	Message   string
}

/*
   Eve Universe ID response
*/
type eveUniverseIDResponse struct {
	Alliances []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"alliances"`
	Characters []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"characters"`
	Corporations []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"corporations"`
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
