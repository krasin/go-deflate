// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build freebsd linux

#include "runtime.h"

// This implementation depends on OS-specific implementations of
//
//	runtime·futexsleep(uint32 *addr, uint32 val, int64 ns)
//		Atomically,
//			if(*addr == val) sleep
//		Might be woken up spuriously; that's allowed.
//		Don't sleep longer than ns; ns < 0 means forever.
//
//	runtime·futexwakeup(uint32 *addr, uint32 cnt)
//		If any procs are sleeping on addr, wake up at most cnt.

enum
{
	MUTEX_UNLOCKED = 0,
	MUTEX_LOCKED = 1,
	MUTEX_SLEEPING = 2,

	ACTIVE_SPIN = 4,
	ACTIVE_SPIN_CNT = 30,
	PASSIVE_SPIN = 1,
};

// Possible lock states are MUTEX_UNLOCKED, MUTEX_LOCKED and MUTEX_SLEEPING.
// MUTEX_SLEEPING means that there is presumably at least one sleeping thread.
// Note that there can be spinning threads during all states - they do not
// affect mutex's state.
void
runtime·lock(Lock *l)
{
	uint32 i, v, wait, spin;

	if(m->locks++ < 0)
		runtime·throw("runtime·lock: lock count");

	// Speculative grab for lock.
	v = runtime·xchg(&l->key, MUTEX_LOCKED);
	if(v == MUTEX_UNLOCKED)
		return;

	// wait is either MUTEX_LOCKED or MUTEX_SLEEPING
	// depending on whether there is a thread sleeping
	// on this mutex.  If we ever change l->key from
	// MUTEX_SLEEPING to some other value, we must be
	// careful to change it back to MUTEX_SLEEPING before
	// returning, to ensure that the sleeping thread gets
	// its wakeup call.
	wait = v;

	// On uniprocessor's, no point spinning.
	// On multiprocessors, spin for ACTIVE_SPIN attempts.
	spin = 0;
	if(runtime·ncpu > 1)
		spin = ACTIVE_SPIN;

	for(;;) {
		// Try for lock, spinning.
		for(i = 0; i < spin; i++) {
			while(l->key == MUTEX_UNLOCKED)
				if(runtime·cas(&l->key, MUTEX_UNLOCKED, wait))
					return;
			runtime·procyield(ACTIVE_SPIN_CNT);
		}

		// Try for lock, rescheduling.
		for(i=0; i < PASSIVE_SPIN; i++) {
			while(l->key == MUTEX_UNLOCKED)
				if(runtime·cas(&l->key, MUTEX_UNLOCKED, wait))
					return;
			runtime·osyield();
		}

		// Sleep.
		v = runtime·xchg(&l->key, MUTEX_SLEEPING);
		if(v == MUTEX_UNLOCKED)
			return;
		wait = MUTEX_SLEEPING;
		runtime·futexsleep(&l->key, MUTEX_SLEEPING, -1);
	}
}

void
runtime·unlock(Lock *l)
{
	uint32 v;

	if(--m->locks < 0)
		runtime·throw("runtime·unlock: lock count");

	v = runtime·xchg(&l->key, MUTEX_UNLOCKED);
	if(v == MUTEX_UNLOCKED)
		runtime·throw("unlock of unlocked lock");
	if(v == MUTEX_SLEEPING)
		runtime·futexwakeup(&l->key, 1);
}

// One-time notifications.
void
runtime·noteclear(Note *n)
{
	n->key = 0;
}

void
runtime·notewakeup(Note *n)
{
	runtime·xchg(&n->key, 1);
	runtime·futexwakeup(&n->key, 1);
}

void
runtime·notesleep(Note *n)
{
	while(runtime·atomicload(&n->key) == 0)
		runtime·futexsleep(&n->key, 0, -1);
}

void
runtime·notetsleep(Note *n, int64 ns)
{
	int64 deadline, now;

	if(ns < 0) {
		runtime·notesleep(n);
		return;
	}

	if(runtime·atomicload(&n->key) != 0)
		return;

	deadline = runtime·nanotime() + ns;
	for(;;) {
		runtime·futexsleep(&n->key, 0, ns);
		if(runtime·atomicload(&n->key) != 0)
			return;
		now = runtime·nanotime();
		if(now >= deadline)
			return;
		ns = deadline - now;
	}
}
