#!/bin/bash

PID=$(pgrep -fo logmonitor.bin)

if [ "$PID" != "" ]; then
  echo 'terminate (signal:15) logmonitor.bin'
  kill -15 $PID
else
  echo 'logmonitor.bin is not running.'
fi
