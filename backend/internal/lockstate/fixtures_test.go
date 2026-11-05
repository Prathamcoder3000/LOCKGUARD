package lockstate

import (
	"fmt"
	"sync/atomic"
	"time"
)

var reqCounter uint64

func nextReqID() string {
	val := atomic.AddUint64(&reqCounter, 1)
	return fmt.Sprintf("req-%06d", val)
}

// Fixed baseline timestamp for deterministic test scenarios: 2026-11-05 18:00:00 UTC
const baseTimeUnixNano int64 = 1793872800000000000

func timeOffset(seconds int64) int64 {
	return baseTimeUnixNano + seconds*int64(time.Second)
}

func timeOffsetMillis(millis int64) int64 {
	return baseTimeUnixNano + millis*int64(time.Millisecond)
}

func makeAcquireCmd(res, owner string, ttlSec int64, tsNano int64) AcquireLockCommand {
	return AcquireLockCommand{
		RequestID:         nextReqID(),
		ResourceName:      res,
		OwnerID:           owner,
		TTLNanoseconds:    ttlSec * int64(time.Second),
		TimestampUnixNano: tsNano,
	}
}

func makeRenewCmd(res, owner string, token uint64, ttlSec int64, tsNano int64) RenewLockCommand {
	return RenewLockCommand{
		RequestID:         nextReqID(),
		ResourceName:      res,
		OwnerID:           owner,
		FencingToken:      token,
		TTLNanoseconds:    ttlSec * int64(time.Second),
		TimestampUnixNano: tsNano,
	}
}

func makeReleaseCmd(res, owner string, token uint64, tsNano int64) ReleaseLockCommand {
	return ReleaseLockCommand{
		RequestID:         nextReqID(),
		ResourceName:      res,
		OwnerID:           owner,
		FencingToken:      token,
		TimestampUnixNano: tsNano,
	}
}

func makeExpireCmd(res, owner string, token uint64, version uint64, expectedExpiryNano int64, tsNano int64) ExpireLockCommand {
	return ExpireLockCommand{
		RequestID:                 nextReqID(),
		ResourceName:              res,
		ExpectedOwnerID:           owner,
		ExpectedFencingToken:      token,
		ExpectedLeaseVersion:      version,
		ExpectedExpiresAtUnixNano: expectedExpiryNano,
		TimestampUnixNano:         tsNano,
	}
}
