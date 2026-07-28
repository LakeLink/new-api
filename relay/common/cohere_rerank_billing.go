package common

import (
	"fmt"
	"math"

	"github.com/QuantumNous/new-api/dto"
)

const (
	// Cohere documents one search unit as one query over at most 100 ranked
	// documents. Long documents are split into chunks, and each chunk counts as
	// a document for billing.
	CohereRerankDocumentsPerSearchUnit = 100
	CohereRerankMaxChunks              = 10_000
	CohereRerankDefaultMaxChunksPerDoc = 10
	CohereRerankMaxSearchUnits         = CohereRerankMaxChunks / CohereRerankDocumentsPerSearchUnit
	CohereRerankSearchUnitsRatioKey    = "cohere_rerank_search_units"
)

// EstimateCohereRerankSearchUnits returns a conservative pre-consume bound.
// Cohere's v1 reference specifies a default of ten chunks per document. The
// endpoint caps the total at 10,000 chunks, so an accepted request can consume
// at most 100 search units.
func EstimateCohereRerankSearchUnits(request *dto.RerankRequest) (int, error) {
	if request == nil {
		return 0, fmt.Errorf("cohere rerank request is nil")
	}
	documentCount := len(request.Documents)
	if documentCount == 0 {
		return 0, fmt.Errorf("cohere rerank documents are empty")
	}
	if documentCount > CohereRerankMaxChunks {
		return 0, fmt.Errorf("cohere rerank documents exceed %d", CohereRerankMaxChunks)
	}

	maxChunksPerDoc := CohereRerankDefaultMaxChunksPerDoc
	explicitMaxChunks := request.GetMaxChunksPerDoc()
	if explicitMaxChunks != nil {
		maxChunksPerDoc = *explicitMaxChunks
		if maxChunksPerDoc <= 0 {
			return 0, fmt.Errorf("cohere rerank max_chunks_per_doc must be at least 1")
		}
		// Division avoids overflowing when the JSON integer is close to MaxInt.
		if documentCount > CohereRerankMaxChunks/maxChunksPerDoc {
			return 0, fmt.Errorf("cohere rerank documents * max_chunks_per_doc must not exceed %d", CohereRerankMaxChunks)
		}
	}

	chunkCount := CohereRerankMaxChunks
	if documentCount <= CohereRerankMaxChunks/maxChunksPerDoc {
		chunkCount = documentCount * maxChunksPerDoc
	}
	return (chunkCount + CohereRerankDocumentsPerSearchUnit - 1) / CohereRerankDocumentsPerSearchUnit, nil
}

// ValidateCohereRerankSearchUnits bounds the provider-controlled billing
// multiplier before it is applied to quota arithmetic.
func ValidateCohereRerankSearchUnits(searchUnits float64) error {
	if math.IsNaN(searchUnits) || math.IsInf(searchUnits, 0) || searchUnits <= 0 {
		return fmt.Errorf("cohere billed search_units must be a positive finite number")
	}
	if math.Trunc(searchUnits) != searchUnits {
		return fmt.Errorf("cohere billed search_units must be a whole number")
	}
	if searchUnits > CohereRerankMaxSearchUnits {
		return fmt.Errorf("cohere billed search_units exceed %d", CohereRerankMaxSearchUnits)
	}
	return nil
}
