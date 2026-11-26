package marketdata

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// InsiderTrade represents a normalized SEC EDGAR Form 4 non-derivative transaction.
type InsiderTrade struct {
	IssuerCIK       string  `json:"issuerCik"`
	IssuerName      string  `json:"issuerName"`
	Ticker          string  `json:"ticker"`
	OwnerName       string  `json:"ownerName"`
	OfficerTitle    string  `json:"officerTitle,omitempty"`
	IsDirector      bool    `json:"isDirector,omitempty"`
	IsOfficer       bool    `json:"isOfficer,omitempty"`
	TransactionDate string  `json:"transactionDate"`
	TransactionCode string  `json:"transactionCode"`
	Action          string  `json:"action"`
	Shares          float64 `json:"shares"`
	PricePerShare   float64 `json:"pricePerShare"`
	NotionalUSD     float64 `json:"notionalUsd"`
	SharesFollowing float64 `json:"sharesFollowing,omitempty"`
}

type xmlForm4Doc struct {
	XMLName xml.Name `xml:"ownershipDocument"`
	Issuer  struct {
		CIK    string `xml:"issuerCik"`
		Name   string `xml:"issuerName"`
		Ticker string `xml:"issuerTradingSymbol"`
	} `xml:"issuer"`
	Owners []struct {
		ID struct {
			Name string `xml:"rptOwnerName"`
		} `xml:"reportingOwnerId"`
		Rel struct {
			IsDirector   string `xml:"isDirector"`
			IsOfficer    string `xml:"isOfficer"`
			OfficerTitle string `xml:"officerTitle"`
		} `xml:"reportingOwnerRelationship"`
	} `xml:"reportingOwner"`
	NonDerivative struct {
		Transactions []struct {
			TxDate struct {
				Value string `xml:"value"`
			} `xml:"transactionDate"`
			Coding struct {
				Code string `xml:"transactionCode"`
			} `xml:"transactionCoding"`
			Amounts struct {
				Shares struct {
					Value string `xml:"value"`
				} `xml:"transactionShares"`
				Price struct {
					Value string `xml:"value"`
				} `xml:"transactionPricePerShare"`
				AcqDisp struct {
					Value string `xml:"value"`
				} `xml:"transactionAcquiredDisposedCode"`
			} `xml:"transactionAmounts"`
			PostAmounts struct {
				SharesFollowing struct {
					Value string `xml:"value"`
				} `xml:"sharesOwnedFollowingTransaction"`
			} `xml:"postTransactionAmounts"`
		} `xml:"nonDerivativeTransaction"`
	} `xml:"nonDerivativeTable"`
}

func parseXMLFlag(raw string) bool {
	s := strings.ToLower(strings.TrimSpace(raw))
	return s == "1" || s == "true" || s == "yes"
}

func parsePositiveXMLFloat(raw string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return 0
	}
	return v
}

// ParseForm4XML decodes an SEC EDGAR Form 4 <ownershipDocument> XML filing into
// bounded, normalized InsiderTrade rows.
func ParseForm4XML(raw []byte) ([]InsiderTrade, error) {
	if len(raw) == 0 || len(raw) > 2<<20 {
		return nil, errors.New("form4 XML payload empty or exceeds 2 MiB limit")
	}
	trimmed := bytes.TrimSpace(raw)
	if !bytes.Contains(trimmed, []byte("ownershipDocument")) {
		return nil, errors.New("not a Form 4 ownershipDocument XML payload")
	}

	var doc xmlForm4Doc
	if err := xml.Unmarshal(trimmed, &doc); err != nil {
		return nil, fmt.Errorf("decode Form 4 XML: %w", err)
	}
	txs := doc.NonDerivative.Transactions
	if len(txs) == 0 {
		return nil, errors.New("form4 XML contains zero non-derivative transactions")
	}

	ownerName := ""
	officerTitle := ""
	isDirector := false
	isOfficer := false
	if len(doc.Owners) > 0 {
		ownerName = strings.TrimSpace(doc.Owners[0].ID.Name)
		officerTitle = strings.TrimSpace(doc.Owners[0].Rel.OfficerTitle)
		isDirector = parseXMLFlag(doc.Owners[0].Rel.IsDirector)
		isOfficer = parseXMLFlag(doc.Owners[0].Rel.IsOfficer)
	}
	cik := strings.TrimSpace(doc.Issuer.CIK)
	issuerName := strings.TrimSpace(doc.Issuer.Name)
	ticker := strings.ToUpper(strings.TrimSpace(doc.Issuer.Ticker))

	out := make([]InsiderTrade, 0, len(txs))
	for _, tx := range txs {
		code := strings.ToUpper(strings.TrimSpace(tx.Coding.Code))
		acqDisp := strings.ToUpper(strings.TrimSpace(tx.Amounts.AcqDisp.Value))
		shares := parsePositiveXMLFloat(tx.Amounts.Shares.Value)
		price := parsePositiveXMLFloat(tx.Amounts.Price.Value)
		following := parsePositiveXMLFloat(tx.PostAmounts.SharesFollowing.Value)

		action := "other"
		switch {
		case code == "P" || (acqDisp == "A" && price > 0):
			action = "buy"
		case code == "S" || acqDisp == "D":
			action = "sell"
		}

		out = append(out, InsiderTrade{
			IssuerCIK:       cik,
			IssuerName:      issuerName,
			Ticker:          ticker,
			OwnerName:       ownerName,
			OfficerTitle:    officerTitle,
			IsDirector:      isDirector,
			IsOfficer:       isOfficer,
			TransactionDate: strings.TrimSpace(tx.TxDate.Value),
			TransactionCode: code,
			Action:          action,
			Shares:          shares,
			PricePerShare:   price,
			NotionalUSD:     math.Round(shares*price*100) / 100,
			SharesFollowing: following,
		})
	}
	return out, nil
}
