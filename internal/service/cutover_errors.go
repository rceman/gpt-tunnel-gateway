package service

import "errors"

var errRunAuthorityRetired = errors.New("Run authority was removed by the Train-v2 hard cutover; address the exact Train item Attempt")
