# Places (Asynchronous Networking)

An application that interacts with public API using asynchronous programming methods. 
In particular, net/http is used to interact with the remote API and sync group to implement asynchronous interaction in the golang language.

## The logic of the work:

In the input field, the user enters the name of something (for example, "Colored Passage") and clicks the search button.;
Location options are searched using the [1] method and shown to the user as a list.;
The user selects one location;
Using the method [2] looking for the weather in the location;
Using the method [3], interesting places in the location are searched, then descriptions are searched for each place found using the method [4].;
Everything found is shown to the user.

## API Methods:

1. [Getting locations with coordinates and names](https://docs.graphhopper.com/#operation/getGeocode)
2. [Getting weather by coordinates](https://openweathermap.org/current)
3. [Getting a list of interesting places by coordinates](https://dev.opentripmap.org/docs#/Objects%20list/getListOfPlacesByRadius)
4. [Getting a place description by its id](https://dev.opentripmap.org/docs#/Object%20properties/getPlaceByXid)


