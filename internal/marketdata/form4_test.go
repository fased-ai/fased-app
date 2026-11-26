package marketdata

import "testing"

const sampleForm4XML = `<?xml version="1.0"?>
<ownershipDocument>
  <schemaVersion>X0508</schemaVersion>
  <issuer>
    <issuerCik>0000320193</issuerCik>
    <issuerName>Apple Inc.</issuerName>
    <issuerTradingSymbol>AAPL</issuerTradingSymbol>
  </issuer>
  <reportingOwner>
    <reportingOwnerId>
      <rptOwnerName>Cook Timothy D</rptOwnerName>
    </reportingOwnerId>
    <reportingOwnerRelationship>
      <isDirector>1</isDirector>
      <isOfficer>1</isOfficer>
      <officerTitle>Chief Executive Officer</officerTitle>
    </reportingOwnerRelationship>
  </reportingOwner>
  <nonDerivativeTable>
    <nonDerivativeTransaction>
      <transactionDate><value>2026-11-20</value></transactionDate>
      <transactionCoding><transactionCode>P</transactionCode></transactionCoding>
      <transactionAmounts>
        <transactionShares><value>1000</value></transactionShares>
        <transactionPricePerShare><value>225.50</value></transactionPricePerShare>
        <transactionAcquiredDisposedCode><value>A</value></transactionAcquiredDisposedCode>
      </transactionAmounts>
      <postTransactionAmounts>
        <sharesOwnedFollowingTransaction><value>3280000</value></sharesOwnedFollowingTransaction>
      </postTransactionAmounts>
    </nonDerivativeTransaction>
    <nonDerivativeTransaction>
      <transactionDate><value>2026-11-21</value></transactionDate>
      <transactionCoding><transactionCode>S</transactionCode></transactionCoding>
      <transactionAmounts>
        <transactionShares><value>500</value></transactionShares>
        <transactionPricePerShare><value>230.00</value></transactionPricePerShare>
        <transactionAcquiredDisposedCode><value>D</value></transactionAcquiredDisposedCode>
      </transactionAmounts>
      <postTransactionAmounts>
        <sharesOwnedFollowingTransaction><value>3279500</value></sharesOwnedFollowingTransaction>
      </postTransactionAmounts>
    </nonDerivativeTransaction>
  </nonDerivativeTable>
</ownershipDocument>`

func TestParseForm4XML(t *testing.T) {
	t.Parallel()
	trades, err := ParseForm4XML([]byte(sampleForm4XML))
	if err != nil {
		t.Fatalf("ParseForm4XML failed: %v", err)
	}
	if len(trades) != 2 {
		t.Fatalf("expected 2 trades, got %d", len(trades))
	}
	if trades[0].Ticker != "AAPL" || !trades[0].IsOfficer || trades[0].Action != "buy" || trades[0].NotionalUSD != 225500 {
		t.Fatalf("unexpected buy trade: %+v", trades[0])
	}
	if trades[1].Action != "sell" || trades[1].Shares != 500 || trades[1].NotionalUSD != 115000 {
		t.Fatalf("unexpected sell trade: %+v", trades[1])
	}

	if _, err := ParseForm4XML([]byte("<notForm4/>")); err == nil {
		t.Fatal("expected rejection for non-Form4 XML")
	}
	if _, err := ParseForm4XML([]byte("<ownershipDocument><issuer/></ownershipDocument>")); err == nil {
		t.Fatal("expected rejection when zero non-derivative transactions exist")
	}
}

func FuzzParseForm4XML(f *testing.F) {
	f.Add([]byte(sampleForm4XML))
	f.Add([]byte("<ownershipDocument><nonDerivativeTable/></ownershipDocument>"))
	f.Add([]byte(""))
	f.Fuzz(func(_ *testing.T, data []byte) {
		_, _ = ParseForm4XML(data)
	})
}
