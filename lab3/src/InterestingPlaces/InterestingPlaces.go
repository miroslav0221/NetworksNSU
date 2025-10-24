package InterestingPlaces

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
	params.Add("lon", strconv.FormatFloat(point.Lng, 'f', -1, 64))
	params.Add("radius", strconv.Itoa(1000)) // 1000 ?
	params.Add("kinds", "interesting_places")
	params.Add("format", "geojson")
	params.Add("apikey", config.OpentripmapAPIKey)
	return config.OpentripmapAPI + "/radius" + "?" + params.Encode()
}

func GetIntPlaces(point src.Point) (*src.PlacesInfo, error) {
	fullUrl := getFullUrl(point)

	resp, err := http.Get(fullUrl)
	if err != nil {
		fmt.Printf("Error fetching data: %v\n", err)
		return nil, err
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("Failed read responce: %v\n", err)
		return nil, err
	}

	fmt.Println(string(body))

	var response src.PlacesInfo
	err = json.Unmarshal(body, &response)
	if err != nil {
		fmt.Println("Failed parse json")
		return nil, err
	}
	return &response, nil
}
