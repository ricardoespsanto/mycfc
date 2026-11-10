#!/usr/bin/env python3
"""Replay the approved two real-plan references through UI on empty synthetic targets."""
import pathlib
import runpy
import sys

runner = pathlib.Path(__file__).with_name('run-focused-water-authoring.py')
sys.argv = [str(runner), '--real-plans', *sys.argv[1:]]
runpy.run_path(str(runner), run_name='__main__')
