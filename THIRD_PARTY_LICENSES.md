# Third-party licenses

## Airwindows Consolidated

`third_party/airwin2rack/src/` is the `src/` folder of Airwindows Consolidated
(airwin2rack, https://github.com/baconpaul/airwin2rack), unchanged except that a
stray build object (`TapeDustProc.o`) was left out. Only `src/` is vendored: no
fonts, images or GPL-licensed resources from that project are included.

License: MIT. Copyright 2019-2026, Paul Walker and the authors in the airwin2rack
git history. Full text: `third_party/airwin2rack/LICENSE.md`.

## Airwindows

The effects themselves are the Airwindows plugins by Chris Johnson
(https://www.airwindows.com, https://github.com/airwindows/airwindows), released
under the MIT license:

    Copyright (c) 2018 Chris Johnson

    Permission is hereby granted, free of charge, to any person obtaining a copy
    of this software and associated documentation files (the "Software"), to deal
    in the Software without restriction, including without limitation the rights
    to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
    copies of the Software, and to permit persons to whom the Software is
    furnished to do so, subject to the following conditions:

    The above copyright notice and this permission notice shall be included in all
    copies or substantial portions of the Software.

    THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
    IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
    FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
    AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
    LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
    OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
    SOFTWARE.

## Push Hack core

`github.com/federico-pepe/ableton-push-hack/core` (a Go module dependency, not
vendored): see the license of https://github.com/federico-pepe/ableton-push-hack.
