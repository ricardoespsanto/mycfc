#!/usr/bin/env python3
"""Run only dated-placement journeys on an owned fresh schema83 fixture."""
import pathlib
import runpy
import sys

runner = pathlib.Path(__file__).with_name('run-focused-water-authoring.py')
sys.argv = [str(runner), '--dated-placements', *sys.argv[1:]]
runpy.run_path(str(runner), run_name='__main__')
