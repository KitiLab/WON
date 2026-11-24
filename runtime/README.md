# Runtime Subsystem

## Overview
This directory contains the minimal runtime support code required by compiled WON executables.

- **Current Status**: `PLANNED`
- **Core Philosophy**: Zero runtime bloat, no garbage collection engine, no GIL.
- **Planned Contents**:
  - Memory allocation wrappers and allocators.
  - Ownership-lite reference counting primitives (`Shared<T>`).
  - Thread spawning, channel primitives, and panic handlers.
  - C ABI bridge utilities.
