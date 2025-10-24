package config

import (
    "os"
)
var ( 

    GraphHopperAPI string
    GraphHopperAPIKey string

    OpentripmapAPI string
    OpentripmapAPIKey string

    OpenweathermapAPI string
    OpenweathermapAPIKey string
 
)


func Init() {
    
    GraphHopperAPI, _ = os.LookupEnv("GRAPHHOPPPER_API")
    GraphHopperAPIKey, _ = os.LookupEnv("GRAPHHOPPPER_API_KEY")

    OpentripmapAPI, _ = os.LookupEnv("OPENTRIPMAP_API")
    OpentripmapAPIKey, _ = os.LookupEnv("OPENTRIPMAP_API_KEY")

    OpenweathermapAPI, _ = os.LookupEnv("OPENWEATHERMAP_API")
    OpenweathermapAPIKey, _ = os.LookupEnv("OPENWEATHERMAP_API_KEY")

}

