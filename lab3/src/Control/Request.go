package Control

import (
	"fmt"
	"lab3/src"
	"lab3/src/InterestingPlaces"
	"lab3/src/Wheather"
	"sync"
)

type Request struct {
	weatherChan (chan *src.Weather)
	placesChan  (chan *src.PlacesInfo)
}

func NewRequest() *Request {
	return &Request{
		weatherChan: make(chan *src.Weather, 1),
		placesChan:  make(chan *src.PlacesInfo, 1),
	}
}

func (req *Request) requestWheather(point src.Point, wg *sync.WaitGroup) {
	defer wg.Done()
	weather, err := Wheather.GetWheather(point)

	if err != nil {
		fmt.Println("Failed to get wheather")
	}

	req.weatherChan <- weather
}

func (req *Request) requestInteresingPlaces(point src.Point, wg *sync.WaitGroup) {
	defer wg.Done()
	intPlaces, err := InterestingPlaces.GetIntPlaces(point)

	if err != nil {
		fmt.Println("Failed to get interesting places")
	}

	req.placesChan <- intPlaces
}

func (req *Request) WaitResults(point src.Point) (*src.Weather, *src.PlacesInfo) {
	var wg sync.WaitGroup
	wg.Add(2)
	go req.requestWheather(point, &wg)
	go req.requestInteresingPlaces(point, &wg)
	wg.Wait()
	wheather := <-req.weatherChan
	places := <-req.placesChan

	return wheather, places
}
