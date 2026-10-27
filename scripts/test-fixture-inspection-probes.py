#!/usr/bin/env python3
"""Test the status reconnect helper without installing or contacting a signer."""
import ast
import contextlib
import pathlib
import types
import unittest


class HistoricalStatusTest(unittest.TestCase):
    def invoke(self, attempts):
        # The probe is an executable UID harness. Compile only this helper so
        # tests cannot import its privileged setup or execute live probes.
        source = pathlib.Path(__file__).with_name("fixture-inspection-probes.py")
        tree = ast.parse(source.read_text())
        helper = next(n for n in tree.body if isinstance(n, ast.FunctionDef)
                      and n.name == "historical_status")
        clock = [0.0]
        self.connections = 0
        self.attempts = iter(attempts)

        def connect():
            self.connections += 1
            return contextlib.nullcontext(next(self.attempts))

        def send(outcome, frame):
            if isinstance(outcome, BrokenPipeError):
                raise outcome

        def read(outcome):
            if isinstance(outcome, Exception):
                raise outcome
            return outcome

        scope = dict(connect=connect, send=send, read=read,
                     os=types.SimpleNamespace(urandom=lambda n: b"a" * n),
                     frame=lambda *args: None, profile="fixture",
                     time=types.SimpleNamespace(monotonic=lambda: clock[0],
                          sleep=lambda seconds: clock.__setitem__(0, clock[0] + seconds)))
        exec(compile(ast.Module(body=[helper], type_ignores=[]), str(source), "exec"), scope)
        scope["historical_status"]()

    def status(self):
        return {"kind": "status-result", "channel": (b"a" * 16).hex(),
                "body": {"historical": True, "fixtureOnly": True, "profile": "fixture"}}

    def test_closed_before_send_reconnects(self):
        self.invoke([BrokenPipeError("previous channel still closing"), self.status()])
        self.assertEqual(self.connections, 2)

    def test_closed_during_read_reconnects(self):
        self.invoke([EOFError("closed"), ConnectionResetError("closed"), self.status()])
        self.assertEqual(self.connections, 3)

    def test_permanent_denial_keeps_original_deadline(self):
        with self.assertRaises(BrokenPipeError):
            self.invoke([BrokenPipeError("busy")] * 200)
        self.assertGreater(self.connections, 1)
        self.assertLessEqual(self.connections, 102)

    def test_authority_response_is_never_retried(self):
        reply = self.status()
        reply["body"]["walletSigner"] = True
        with self.assertRaises(AssertionError):
            self.invoke([reply, self.status()])
        self.assertEqual(self.connections, 1)


if __name__ == "__main__":
    unittest.main()
