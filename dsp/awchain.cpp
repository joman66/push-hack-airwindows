/*
 * awchain.cpp - see awchain.h.
 *
 * Built against third_party/airwin2rack/src (Airwindows Consolidated by Paul
 * Walker and contributors, MIT; Airwindows by Chris Johnson, MIT).
 */
#include "awchain.h"

#include "AirwinRegistry.h"

#include <algorithm>
#include <array>
#include <atomic>
#include <chrono>
#include <cmath>
#include <cstdio>
#include <cstring>
#include <memory>
#include <mutex>
#include <xmmintrin.h>
#include <pmmintrin.h>

namespace
{
std::once_flag regOnce;

void initRegistry()
{
    std::call_once(regOnce, []() {
        // Effects the registry has no description for are already dropped by
        // registerAirwindow. Drop "Unclassified" too: those are unfinished.
        AirwinRegistry::filterAndRebuildRegistry(
            [](const AirwinRegistry::awReg &r) { return r.category == "Unclassified"; });
    });
}

bool validEffect(int i)
{
    initRegistry();
    return i >= 0 && i < (int)AirwinRegistry::registry.size();
}

void copyText(const std::string &s, char *buf, int len)
{
    if (!buf || len <= 0)
        return;
    std::snprintf(buf, (size_t)len, "%s", s.c_str());
}

// Airwindows pads numbers ("%8.4f"); trim both ends.
std::string trim(const char *s)
{
    std::string t(s);
    auto b = t.find_first_not_of(" \t");
    if (b == std::string::npos)
        return "";
    auto e = t.find_last_not_of(" \t");
    return t.substr(b, e - b + 1);
}

constexpr int kBlock = 1024; // largest chunk handed to one processReplacing call

struct Slot
{
    int effect{-1};
    std::unique_ptr<AirwinConsolidatedBase> fx;
    std::atomic<int> bypass{0};
};

std::unique_ptr<Slot> makeSlot(int effect, float sr)
{
    if (!validEffect(effect))
        return nullptr;
    // Some effects read the rate in their constructor.
    AirwinConsolidatedBase::defaultSampleRate = sr;
    auto s = std::make_unique<Slot>();
    s->effect = effect;
    s->fx = AirwinRegistry::registry[effect].generator();
    if (!s->fx)
        return nullptr;
    s->fx->setSampleRate(sr);
    return s;
}
} // namespace

struct aw_chain
{
    float sr{44100.f};
    std::mutex m;
    std::array<std::unique_ptr<Slot>, AW_MAX_SLOTS> slots{};
    int n{0};

    std::atomic<float> inGain{1.f}, outGain{1.f};

    // audio thread only
    float bufA[2][kBlock];
    float bufB[2][kBlock];

    // stats, written by the audio thread under m
    float worstUs{0}, peakIn{0}, peakOut{0};
    int bad{0};

    Slot *at(int pos) { return (pos >= 0 && pos < n) ? slots[pos].get() : nullptr; }
};

extern "C" {

int aw_count(void)
{
    initRegistry();
    return (int)AirwinRegistry::registry.size();
}

void aw_name(int i, char *buf, int len)
{
    copyText(validEffect(i) ? AirwinRegistry::registry[i].name : "", buf, len);
}

void aw_category(int i, char *buf, int len)
{
    copyText(validEffect(i) ? AirwinRegistry::registry[i].category : "", buf, len);
}

void aw_what(int i, char *buf, int len)
{
    copyText(validEffect(i) ? AirwinRegistry::registry[i].whatText : "", buf, len);
}

int aw_nparams(int i) { return validEffect(i) ? AirwinRegistry::registry[i].nParams : 0; }

int aw_chris_order(int i) { return validEffect(i) ? AirwinRegistry::registry[i].catChrisOrdering : 0; }

int aw_find(const char *name)
{
    initRegistry();
    if (!name)
        return -1;
    auto it = AirwinRegistry::nameToIndex.find(name);
    return it == AirwinRegistry::nameToIndex.end() ? -1 : it->second;
}

aw_chain *chain_new(float sample_rate)
{
    initRegistry();
    auto *c = new aw_chain();
    c->sr = sample_rate > 2000 ? sample_rate : 44100.f;
    return c;
}

void chain_free(aw_chain *c) { delete c; }

int chain_len(aw_chain *c)
{
    std::lock_guard<std::mutex> g(c->m);
    return c->n;
}

int chain_slot_effect(aw_chain *c, int pos)
{
    std::lock_guard<std::mutex> g(c->m);
    auto *s = c->at(pos);
    return s ? s->effect : -1;
}

int chain_insert(aw_chain *c, int pos, int effect)
{
    auto s = makeSlot(effect, c->sr); // allocate outside the lock
    if (!s)
        return -1;
    std::lock_guard<std::mutex> g(c->m);
    if (c->n >= AW_MAX_SLOTS || pos < 0 || pos > c->n)
        return -1;
    for (int i = c->n; i > pos; --i)
        c->slots[i] = std::move(c->slots[i - 1]);
    c->slots[pos] = std::move(s);
    c->n++;
    return 0;
}

int chain_replace(aw_chain *c, int pos, int effect)
{
    auto s = makeSlot(effect, c->sr);
    if (!s)
        return -1;
    std::unique_ptr<Slot> old;
    {
        std::lock_guard<std::mutex> g(c->m);
        if (!c->at(pos))
            return -1;
        s->bypass.store(c->slots[pos]->bypass.load());
        old = std::move(c->slots[pos]);
        c->slots[pos] = std::move(s);
    }
    return 0; // old is freed here, outside the lock
}

int chain_remove(aw_chain *c, int pos)
{
    std::unique_ptr<Slot> old;
    {
        std::lock_guard<std::mutex> g(c->m);
        if (!c->at(pos))
            return -1;
        old = std::move(c->slots[pos]);
        for (int i = pos; i < c->n - 1; ++i)
            c->slots[i] = std::move(c->slots[i + 1]);
        c->n--;
    }
    return 0;
}

int chain_move(aw_chain *c, int from, int to)
{
    std::lock_guard<std::mutex> g(c->m);
    if (!c->at(from) || to < 0 || to >= c->n)
        return -1;
    if (from == to)
        return 0;
    auto s = std::move(c->slots[from]);
    if (from < to)
        for (int i = from; i < to; ++i)
            c->slots[i] = std::move(c->slots[i + 1]);
    else
        for (int i = from; i > to; --i)
            c->slots[i] = std::move(c->slots[i - 1]);
    c->slots[to] = std::move(s);
    return 0;
}

void chain_clear(aw_chain *c)
{
    std::array<std::unique_ptr<Slot>, AW_MAX_SLOTS> old;
    {
        std::lock_guard<std::mutex> g(c->m);
        for (int i = 0; i < c->n; ++i)
            old[i] = std::move(c->slots[i]);
        c->n = 0;
    }
}

void chain_set_bypass(aw_chain *c, int pos, int bypass)
{
    std::lock_guard<std::mutex> g(c->m);
    if (auto *s = c->at(pos))
        s->bypass.store(bypass ? 1 : 0);
}

int chain_bypass(aw_chain *c, int pos)
{
    std::lock_guard<std::mutex> g(c->m);
    auto *s = c->at(pos);
    return s ? s->bypass.load() : 0;
}

int chain_param_count(aw_chain *c, int pos)
{
    std::lock_guard<std::mutex> g(c->m);
    auto *s = c->at(pos);
    return s ? AirwinRegistry::registry[s->effect].nParams : 0;
}

float chain_param_get(aw_chain *c, int pos, int p)
{
    std::lock_guard<std::mutex> g(c->m);
    auto *s = c->at(pos);
    if (!s || p < 0 || p >= AirwinRegistry::registry[s->effect].nParams)
        return 0.f;
    return s->fx->getParameter(p);
}

void chain_param_set(aw_chain *c, int pos, int p, float v)
{
    v = std::min(1.f, std::max(0.f, v));
    std::lock_guard<std::mutex> g(c->m);
    auto *s = c->at(pos);
    if (!s || p < 0 || p >= AirwinRegistry::registry[s->effect].nParams)
        return;
    s->fx->setParameter(p, v);
}

void chain_param_name(aw_chain *c, int pos, int p, char *buf, int len)
{
    char txt[256] = {0};
    {
        std::lock_guard<std::mutex> g(c->m);
        auto *s = c->at(pos);
        if (s && p >= 0 && p < AirwinRegistry::registry[s->effect].nParams)
            s->fx->getParameterName(p, txt);
    }
    copyText(trim(txt), buf, len);
}

void chain_param_display(aw_chain *c, int pos, int p, char *buf, int len)
{
    char disp[256] = {0}, label[256] = {0};
    {
        std::lock_guard<std::mutex> g(c->m);
        auto *s = c->at(pos);
        if (s && p >= 0 && p < AirwinRegistry::registry[s->effect].nParams)
        {
            s->fx->getParameterDisplay(p, disp);
            s->fx->getParameterLabel(p, label);
        }
    }
    auto d = trim(disp), l = trim(label);
    copyText(l.empty() ? d : d + " " + l, buf, len);
}

void chain_set_gains(aw_chain *c, float in_gain, float out_gain)
{
    c->inGain.store(in_gain);
    c->outGain.store(out_gain);
}

static float peakOf(const float *x, int n, float pk)
{
    for (int i = 0; i < n; ++i)
        pk = std::max(pk, std::fabs(x[i]));
    return pk;
}

void chain_process(aw_chain *c, float *io, int frames)
{
    // Flush denormals: some effects ring out for a long time on silence.
    _MM_SET_FLUSH_ZERO_MODE(_MM_FLUSH_ZERO_ON);
    _MM_SET_DENORMALS_ZERO_MODE(_MM_DENORMALS_ZERO_ON);

    auto t0 = std::chrono::steady_clock::now();
    const float ig = c->inGain.load(), og = c->outGain.load();

    std::lock_guard<std::mutex> g(c->m);
    for (int off = 0; off < frames; off += kBlock)
    {
        const int n = std::min(kBlock, frames - off);
        float *x = io + off * 2;

        for (int i = 0; i < n; ++i)
        {
            c->bufA[0][i] = x[2 * i] * ig;
            c->bufA[1][i] = x[2 * i + 1] * ig;
        }
        c->peakIn = peakOf(c->bufA[0], n, peakOf(c->bufA[1], n, c->peakIn));

        float *inP[2] = {c->bufA[0], c->bufA[1]};
        float *outP[2] = {c->bufB[0], c->bufB[1]};
        for (int s = 0; s < c->n; ++s)
        {
            Slot *sl = c->slots[s].get();
            if (sl->bypass.load())
                continue;
            sl->fx->processReplacing(inP, outP, n);
            std::swap(inP[0], outP[0]);
            std::swap(inP[1], outP[1]);
        }

        for (int i = 0; i < n; ++i)
        {
            float l = inP[0][i] * og, r = inP[1][i] * og;
            if (!std::isfinite(l)) { l = 0.f; c->bad++; }
            if (!std::isfinite(r)) { r = 0.f; c->bad++; }
            x[2 * i] = l;
            x[2 * i + 1] = r;
        }
        c->peakOut = peakOf(x, n * 2, c->peakOut);
    }

    auto us = std::chrono::duration<float, std::micro>(std::chrono::steady_clock::now() - t0).count();
    c->worstUs = std::max(c->worstUs, us);
}

void chain_stats(aw_chain *c, float *worst_us, float *peak_in, float *peak_out, int *bad)
{
    std::lock_guard<std::mutex> g(c->m);
    if (worst_us) *worst_us = c->worstUs;
    if (peak_in) *peak_in = c->peakIn;
    if (peak_out) *peak_out = c->peakOut;
    if (bad) *bad = c->bad;
    c->worstUs = c->peakIn = c->peakOut = 0;
    c->bad = 0;
}

} // extern "C"
