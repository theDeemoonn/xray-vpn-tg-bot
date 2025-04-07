package repository

import "errors"

// ErrPlanExists indicates that a plan with the given name already exists.
var ErrPlanExists = errors.New("plan with this name already exists")
