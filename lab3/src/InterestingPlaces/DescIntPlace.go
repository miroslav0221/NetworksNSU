package InterestingPlaces

import (
	"encoding/json"
	"fmt"
	"io"
	"lab3/src"
	"net/http"
	"net/url"
	"lab3/cmd/config"
)


func getFullDesclUrl(xid string) string {
	params := url.Values{}
	params.Add("apikey", config.OpentripmapAPIKey)
	return config.OpentripmapAPI + "/xid/" + xid + "?" + params.Encode()
}

func GetDescPlaces(xid string) (*src.Description, error) {
	fullUrl := getFullDesclUrl(xid)

	resp, err := http.Get(fullUrl)
	if err != nil {
		fmt.Printf("Error fetching data: %v\n", err)
		return nil, err
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	var response src.Description
	err = json.Unmarshal(body, &response)
	if err != nil {
		fmt.Println("Failed parse json")
		return nil, err
	}
	return &response, nil

}
