package Wheather

import (
	"encoding/json"
	"fmt"
	"io"
	"lab3/src"
	"net/http"
	"net/url"
	"strconv"
	"lab3/cmd/config"
)


func getFullUrl(point src.Point) string {
	params := url.Values{}
	params.Add("lat", strconv.FormatFloat(point.Lat, 'f', -1, 64))
	params.Add("lon", strconv.FormatFloat(point.Lat, 'f', -1, 64))
	params.Add("appid", config.OpenweathermapAPIKey)
	return config.OpenweathermapAPI + "?" + params.Encode()
}

func GetWheather(point src.Point) (*src.Weather, error) {
	fullUrl := getFullUrl(point)

	resp, err := http.Get(fullUrl)
	if err != nil {
		fmt.Printf("Error fetching wheather: %v\n", err)
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)

	if err != nil {
		fmt.Printf("Failed read body : %v\n", err)
		return nil, err
	}

	var response src.Weather
	err = json.Unmarshal(body, &response)
	if err != nil {
		fmt.Printf("Error fetching wheather: %v\n", err)
		return nil, err
	}

	return &response, nil
}
