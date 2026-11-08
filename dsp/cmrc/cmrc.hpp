// Stand-in for CMakeRC, so third_party/airwin2rack/src/AirwinRegistry.cpp
// builds unchanged. The long Airwindows documentation is not embedded:
// documentationStringFor returns "". The hack shows each effect's one-line
// description from the registry instead.
#pragma once
#include <string>

namespace cmrc
{
struct file
{
    const char *begin() const { return nullptr; }
    const char *end() const { return nullptr; }
};
struct embedded_filesystem
{
    bool is_file(const std::string &) const { return false; }
    file open(const std::string &) const { return {}; }
};
} // namespace cmrc

#define CMRC_DECLARE(ns)                                                                           \
    namespace cmrc                                                                                 \
    {                                                                                              \
    namespace ns                                                                                   \
    {                                                                                              \
    inline embedded_filesystem get_filesystem() { return {}; }                                     \
    }                                                                                              \
    }
