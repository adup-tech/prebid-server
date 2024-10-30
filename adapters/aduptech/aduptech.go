package aduptech

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/prebid/openrtb/v20/openrtb2"
	"github.com/prebid/prebid-server/v2/adapters"
	"github.com/prebid/prebid-server/v2/config"
	"github.com/prebid/prebid-server/v2/currency"
	"github.com/prebid/prebid-server/v2/errortypes"
	"github.com/prebid/prebid-server/v2/openrtb_ext"
)

type adapter struct {
	endpoint   string
	bidderName string
}

type BidExt struct {
	Prebid ExtPrebid `json:"prebid"`
}

type ExtPrebid struct {
	BidType     openrtb_ext.BidType `json:"type"`
	NetworkName string              `json:"networkName"`
}

type aduptechImpExt struct {
	Aduptech openrtb_ext.ExtImpAduptech `json:"aduptech"`
}

const TARGET_CURRENCY = "EUR"

// Builder builds a new instance of the {bidder} adapter for the given bidder with the given config.
func Builder(bidderName openrtb_ext.BidderName, config config.Adapter, server config.Server) (adapters.Bidder, error) {
	bidder := &adapter{
		endpoint:   config.Endpoint,
		bidderName: string(bidderName),
	}
	return bidder, nil
}

func (a *adapter) MakeRequests(request *openrtb2.BidRequest, reqInfo *adapters.ExtraRequestInfo) ([]*adapters.RequestData, []error) {
	for i := range request.Imp {
		imp := &request.Imp[i]
		// Check if imp comes with bid floor amount defined in a foreign currency
		if imp.BidFloor > 0 && imp.BidFloorCur != "" && strings.ToUpper(imp.BidFloorCur) != TARGET_CURRENCY {

			// Convert to TARGET_CURRENCY
			convertedValue, err := reqInfo.ConvertCurrency(imp.BidFloor, imp.BidFloorCur, TARGET_CURRENCY)

			if err != nil {
				var convErr currency.ConversionNotFoundError
				if errors.As(err, &convErr) {
					// try again by first converting to USD
					convertedValue, err = reqInfo.ConvertCurrency(imp.BidFloor, imp.BidFloorCur, "USD")

					if err != nil {
						return nil, []error{err}
					}

					// then convert to TARGET_CURRENCY
					convertedValue, err = reqInfo.ConvertCurrency(convertedValue, "USD", TARGET_CURRENCY)

					if err != nil {
						return nil, []error{err}
					}
				} else {
					return nil, []error{err}
				}
			}

			imp.BidFloorCur = TARGET_CURRENCY
			imp.BidFloor = convertedValue

		}
	}

	url, err := a.buildEndpointURL(&imp)
	if err != nil {
		errors = append(errors, err)
		continue
	}

	requestJSON, err := json.Marshal(request)
	if err != nil {
		return nil, []error{err}
	}

	fmt.Println(string(requestJSON))

	requestData := &adapters.RequestData{
		Method: "POST",
		Uri:    url,
		Body:   requestJSON,
		ImpIDs: openrtb_ext.GetImpIDs(request.Imp),
	}

	return []*adapters.RequestData{requestData}, nil
}

// Builds endpoint url based on adapter-specific pub settings from imp.ext
func (a *adapter) buildEndpointURL(params *openrtb_ext.ExtImpAduptech) (string, error) {
	endpointParams := macros.EndpointTemplateParams{publisher: params.PublisherId}
	return macros.ResolveMacros(a.endpoint, endpointParams)
}

func (a *adapter) MakeBids(request *openrtb2.BidRequest, requestData *adapters.RequestData, responseData *adapters.ResponseData) (*adapters.BidderResponse, []error) {
	if responseData.StatusCode == http.StatusNoContent {
		return nil, nil
	}

	if responseData.StatusCode == http.StatusBadRequest {
		err := &errortypes.BadInput{
			Message: "Unexpected status code: 400. Run with request.debug = 1 for more info.",
		}
		return nil, []error{err}
	}

	if responseData.StatusCode != http.StatusOK {
		err := &errortypes.BadServerResponse{
			Message: fmt.Sprintf("Unexpected status code: %d. Run with request.debug = 1 for more info.", responseData.StatusCode),
		}
		return nil, []error{err}
	}

	var response openrtb2.BidResponse
	if err := json.Unmarshal(responseData.Body, &response); err != nil {
		return nil, []error{err}
	}

	fmt.Println("response: ", response)

	bidResponse := adapters.NewBidderResponse()
	bidResponse.Currency = response.Cur

	for _, seatBid := range response.SeatBid {
		for i := range seatBid.Bid {
			var bidExt BidExt
			if err := json.Unmarshal(seatBid.Bid[i].Ext, &bidExt); err != nil {
				return nil, []error{&errortypes.BadServerResponse{
					Message: fmt.Sprintf("Missing ext.prebid.type in bid for impression : %s.", seatBid.Bid[i].ImpID),
				}}
			}

			b := &adapters.TypedBid{
				Bid:     &seatBid.Bid[i],
				BidType: "native",
			}
			bidResponse.Bids = append(bidResponse.Bids, b)
		}
	}

	return bidResponse, nil
}
