// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package middleware

// Compatibility aliases: the authentication middleware moved to
// pkg/authmw in the T-19 extraction. These aliases preserve every
// pre-existing reference (types are aliased, so context-key lookups
// remain type-identical). New code should import pkg/authmw directly.

import (
	"github.com/ha1tch/xolu/pkg/authmw"
)

type ContextKey = authmw.ContextKey

const (
	ContextKeySubject     = authmw.ContextKeySubject
	ContextKeyAuthMethod  = authmw.ContextKeyAuthMethod
	ContextKeyTenantGrant = authmw.ContextKeyTenantGrant
)

type (
	JWTClaims   = authmw.JWTClaims
	TenantGrant = authmw.TenantGrant
)

var (
	AuthMiddleware         = authmw.AuthMiddleware
	GetSubject             = authmw.GetSubject
	GetAuthMethod          = authmw.GetAuthMethod
	TenantGrantFromContext = authmw.TenantGrantFromContext
)
