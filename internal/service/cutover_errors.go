package service

import "errors"

var errRunAuthorityRetired = errors.New("Run authority was removed by the execution hard cutover; the canonical Task execution lifecycle is the only execution authority")
