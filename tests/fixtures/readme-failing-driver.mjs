#!/usr/bin/env node

process.stderr.write('Intentional README lifecycle fixture failure: scene=runtime-probe rule=fixture-failure.\n');
process.exitCode = 7;
