package main

import (
	"fmt"
	"os"

	"github.com/onrik/logrus/filename"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

/*
   Configure logging based on Viper config and return logging object
*/
func ConfigureLogging(viper *viper.Viper) (logger *log.Logger) {
	// Parse log level
	level, err := log.ParseLevel(viper.GetString("log_level"))
	if err != nil {
		fmt.Printf("Unable to parse human log level in config: %s \n", err)
		os.Exit(1)
	}
	log.SetLevel(level)

	// Log to File Support
	if viper.GetBool("log_to_file") {
		// Attempt to open/create file, fall back to console and log failure
		logFile, err := os.OpenFile(viper.GetString("log_file_path"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0666)
		if err != nil {
			fmt.Printf("Failed to open log file path, defaulting to console: %s", err)
			log.SetOutput(os.Stdout)
		} else {
			log.SetOutput(logFile)
		}
	} else {
		log.SetOutput(os.Stdout)
	}

	// Log filename and lines
	if log.GetLevel() == log.DebugLevel {
		filenameHook := filename.NewHook()
		filenameHook.Field = "fileline"
		log.AddHook(filenameHook)
	}

	return log.StandardLogger()
}
