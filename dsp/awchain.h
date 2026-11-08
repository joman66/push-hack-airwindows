/*
 * awchain.h - C API over the Airwindows Consolidated registry.
 *
 * Two parts:
 *   aw_*     read-only catalog of every Airwindows effect (name, category,
 *            one-line description, parameter count).
 *   chain_*  an ordered list of effect instances that processes stereo audio.
 *
 * Threading: chain_process runs on the audio thread. Every other chain_* call
 * may run on any other thread. Insert/remove/move build or free the plugin
 * outside the lock and only swap pointers under it, so the audio thread never
 * waits for an allocation. Parameter reads and writes take the same short lock,
 * so a slot cannot be freed while the UI reads it.
 */
#ifndef AWCHAIN_H
#define AWCHAIN_H

#ifdef __cplusplus
extern "C" {
#endif

#define AW_MAX_SLOTS 8
#define AW_TEXT 64

/* ---- catalog ---- */

/* Number of effects in the catalog. Builds the registry on first call. */
int aw_count(void);
/* Copy a field of effect i into buf (size AW_TEXT, or 256 for what). */
void aw_name(int i, char *buf, int len);
void aw_category(int i, char *buf, int len);
void aw_what(int i, char *buf, int len);
int aw_nparams(int i);
/* Airwindows' own order inside a category (lower = more recommended). */
int aw_chris_order(int i);
/* Catalog index for a name, or -1. */
int aw_find(const char *name);

/* ---- chain ---- */

typedef struct aw_chain aw_chain;

aw_chain *chain_new(float sample_rate);
void chain_free(aw_chain *c);

int chain_len(aw_chain *c);
/* Catalog index of the effect in slot pos, or -1. */
int chain_slot_effect(aw_chain *c, int pos);

/* Insert effect `effect` at pos (0..len). Returns 0, or -1 if the chain is
 * full, pos is out of range, or the effect does not exist. */
int chain_insert(aw_chain *c, int pos, int effect);
/* Replace the effect in slot pos. Keeps the slot's bypass state. */
int chain_replace(aw_chain *c, int pos, int effect);
int chain_remove(aw_chain *c, int pos);
int chain_move(aw_chain *c, int from, int to);
void chain_clear(aw_chain *c);

void chain_set_bypass(aw_chain *c, int pos, int bypass);
int chain_bypass(aw_chain *c, int pos);

int chain_param_count(aw_chain *c, int pos);
float chain_param_get(aw_chain *c, int pos, int p);
void chain_param_set(aw_chain *c, int pos, int p, float v);
void chain_param_name(aw_chain *c, int pos, int p, char *buf, int len);
/* Value as the effect prints it, with its unit ("-3.2 dB"). */
void chain_param_display(aw_chain *c, int pos, int p, char *buf, int len);

/* Gains are linear. Applied before the first and after the last slot. */
void chain_set_gains(aw_chain *c, float in_gain, float out_gain);

/* Process n frames of interleaved stereo float in place. */
void chain_process(aw_chain *c, float *interleaved, int n);

/* Stats since the last call: worst block time in microseconds, peak level
 * in and out (linear), and how many non-finite samples were silenced. */
void chain_stats(aw_chain *c, float *worst_us, float *peak_in, float *peak_out, int *bad);

#ifdef __cplusplus
}
#endif
#endif
