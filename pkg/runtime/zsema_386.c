// AUTO-GENERATED by autogen.sh; DO NOT EDIT

#include "runtime.h"
#include "arch_GOARCH.h"

#line 24 "sema.goc"
typedef struct Sema Sema; 
struct Sema 
{ 
uint32 volatile *addr; 
G *g; 
Sema *prev; 
Sema *next; 
} ; 
#line 33 "sema.goc"
typedef struct SemaRoot SemaRoot; 
struct SemaRoot 
{ 
Lock; 
Sema *head; 
Sema *tail; 
#line 40 "sema.goc"
uint32 volatile nwait; 
} ; 
#line 44 "sema.goc"
#define SEMTABLESZ 251 
#line 46 "sema.goc"
static union 
{ 
SemaRoot; 
uint8 pad[CacheLineSize]; 
} semtable[SEMTABLESZ]; 
#line 52 "sema.goc"
static SemaRoot* 
semroot ( uint32 *addr ) 
{ 
return &semtable[ ( ( uintptr ) addr >> 3 ) % SEMTABLESZ]; 
} 
#line 58 "sema.goc"
static void 
semqueue ( SemaRoot *root , uint32 volatile *addr , Sema *s ) 
{ 
s->g = g; 
s->addr = addr; 
s->next = nil; 
s->prev = root->tail; 
if ( root->tail ) 
root->tail->next = s; 
else 
root->head = s; 
root->tail = s; 
} 
#line 72 "sema.goc"
static void 
semdequeue ( SemaRoot *root , Sema *s ) 
{ 
if ( s->next ) 
s->next->prev = s->prev; 
else 
root->tail = s->prev; 
if ( s->prev ) 
s->prev->next = s->next; 
else 
root->head = s->next; 
s->prev = nil; 
s->next = nil; 
} 
#line 87 "sema.goc"
static int32 
cansemacquire ( uint32 *addr ) 
{ 
uint32 v; 
#line 92 "sema.goc"
while ( ( v = runtime·atomicload ( addr ) ) > 0 ) 
if ( runtime·cas ( addr , v , v-1 ) ) 
return 1; 
return 0; 
} 
#line 98 "sema.goc"
void 
runtime·semacquire ( uint32 volatile *addr ) 
{ 
Sema s; 
SemaRoot *root; 
#line 105 "sema.goc"
if ( cansemacquire ( addr ) ) 
return; 
#line 114 "sema.goc"
root = semroot ( addr ) ; 
for ( ;; ) { 
runtime·lock ( root ) ; 
#line 118 "sema.goc"
runtime·xadd ( &root->nwait , 1 ) ; 
#line 120 "sema.goc"
if ( cansemacquire ( addr ) ) { 
runtime·xadd ( &root->nwait , -1 ) ; 
runtime·unlock ( root ) ; 
return; 
} 
#line 127 "sema.goc"
semqueue ( root , addr , &s ) ; 
g->status = Gwaiting; 
g->waitreason = "semacquire" ; 
runtime·unlock ( root ) ; 
runtime·gosched ( ) ; 
if ( cansemacquire ( addr ) ) 
return; 
} 
} 
#line 137 "sema.goc"
void 
runtime·semrelease ( uint32 volatile *addr ) 
{ 
Sema *s; 
SemaRoot *root; 
#line 143 "sema.goc"
root = semroot ( addr ) ; 
runtime·xadd ( addr , 1 ) ; 
#line 149 "sema.goc"
if ( runtime·atomicload ( &root->nwait ) == 0 ) 
return; 
#line 153 "sema.goc"
runtime·lock ( root ) ; 
if ( runtime·atomicload ( &root->nwait ) == 0 ) { 
#line 157 "sema.goc"
runtime·unlock ( root ) ; 
return; 
} 
for ( s = root->head; s; s = s->next ) { 
if ( s->addr == addr ) { 
runtime·xadd ( &root->nwait , -1 ) ; 
semdequeue ( root , s ) ; 
break; 
} 
} 
runtime·unlock ( root ) ; 
if ( s ) 
runtime·ready ( s->g ) ; 
} 
void
runtime·Semacquire(uint32* addr)
{
#line 172 "sema.goc"

	runtime·semacquire(addr);
}
void
runtime·Semrelease(uint32* addr)
{
#line 176 "sema.goc"

	runtime·semrelease(addr);
}