package client

import (
	"context"
	"fmt"
)

// PaginatorInfo mirrors the GraphQL PaginatorInfo type used by Worksome's API.
type PaginatorInfo struct {
	Count        int  `json:"count"`
	CurrentPage  int  `json:"currentPage"`
	HasMorePages bool `json:"hasMorePages"`
	LastPage     int  `json:"lastPage"`
	PerPage      int  `json:"perPage"`
	Total        int  `json:"total"`
}

// PaginatedResponse represents a paginated GraphQL response where Data holds the
// page items and PaginatorInfo describes the pagination state.
type PaginatedResponse[T any] struct {
	Data          []T           `json:"data"`
	PaginatorInfo PaginatorInfo `json:"paginatorInfo"`
}

// PaginatedResult is a helper type used to extract a PaginatedResponse from a
// GraphQL response envelope. The caller's query must alias the paginated field
// so that it unmarshals into the "result" key.
type PaginatedResult[T any] struct {
	Result PaginatedResponse[T] `json:"result"`
}

// ExecuteAll runs a paginated GraphQL query repeatedly, incrementing the "page"
// variable, until all pages have been fetched. It returns the concatenated items
// from every page.
//
// The query MUST:
//   - Accept a $page: Int! variable
//   - Alias the paginated field as "result"
//   - Select paginatorInfo { hasMorePages currentPage } inside "result"
//   - Select data { ... } inside "result"
//
// Example query:
//
//	query($page: Int!) {
//	  result: workers(page: $page, first: 50) {
//	    data { id name }
//	    paginatorInfo { hasMorePages currentPage }
//	  }
//	}
func ExecuteAll[T any](ctx context.Context, c *Client, query string, variables map[string]any) ([]T, error) {
	if variables == nil {
		variables = make(map[string]any)
	}

	var all []T
	page := 1

	for {
		variables["page"] = page

		var envelope PaginatedResult[T]
		if err := c.Execute(ctx, query, variables, &envelope); err != nil {
			return all, fmt.Errorf("page %d: %w", page, err)
		}

		all = append(all, envelope.Result.Data...)

		if !envelope.Result.PaginatorInfo.HasMorePages {
			break
		}

		page++
	}

	return all, nil
}
