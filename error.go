package main

import (
	log "github.com/sirupsen/logrus"
)

type AppErr struct {
	err    error
	msg    string
	fields log.Fields
}

func NewAppErr(err error, msg string, fields log.Fields) error {
	return &AppErr{
		err:    err,
		msg:    msg,
		fields: fields,
	}
}

func (a *AppErr) Error() string {
	if a.err == nil {
		return a.msg
	}
	return a.msg + ": " + a.err.Error()
}
