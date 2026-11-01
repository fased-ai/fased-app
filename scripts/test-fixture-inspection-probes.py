#!/usr/bin/env python3
"""Test socket denial/reconnect without installing or contacting a signer."""
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


class ChannelDenialTest(unittest.TestCase):
    def probe(self, send_error=None, read_error=None, reply=None, attempt=True):
        source = pathlib.Path(__file__).with_name("fixture-inspection-probes.py")
        tree = ast.parse(source.read_text())
        helper = next(n for n in tree.body if isinstance(n, ast.FunctionDef)
                      and n.name == "denied")
        self.calls = []
        connection = object()
        request = {"kind": "status"}

        def send(actual, frame):
            self.assertIs(actual, connection)
            self.assertIs(frame, request)
            self.calls.append("send")
            if send_error is not None:
                raise send_error

        def read(actual):
            self.assertIs(actual, connection)
            self.calls.append("read")
            if read_error is not None:
                raise read_error
            return reply

        scope = dict(send=send, read=read)
        exec(compile(ast.Module(body=[helper], type_ignores=[]), str(source), "exec"), scope)
        return scope["denied"](connection, request) if attempt else scope["denied"](connection)

    def test_closed_before_send_is_denied_without_retry(self):
        for error in (BrokenPipeError("closed"), ConnectionResetError("closed")):
            with self.subTest(error=type(error).__name__):
                self.assertTrue(self.probe(send_error=error))
                self.assertEqual(self.calls, ["send"])

    def test_closed_during_read_is_denied_without_retry(self):
        for error in (EOFError("closed"), ConnectionResetError("closed"), BrokenPipeError("closed")):
            with self.subTest(error=type(error).__name__):
                self.assertTrue(self.probe(read_error=error))
                self.assertEqual(self.calls, ["send", "read"])

    def test_any_response_fails_the_denial_probe(self):
        for reply in (None, {}, {"kind": "status-result"}, {"kind": "challenge"}):
            with self.subTest(reply=reply):
                self.assertFalse(self.probe(reply=reply))
                self.assertEqual(self.calls, ["send", "read"])

    def test_timeout_and_other_errors_are_not_denial(self):
        for error in (TimeoutError("deadline"), PermissionError("permission"),
                      ConnectionRefusedError("refused"), OSError("unexpected"), ValueError("invalid")):
            for phase in ("send_error", "read_error"):
                with self.subTest(error=type(error).__name__, phase=phase):
                    with self.assertRaises(type(error)):
                        self.probe(**{phase: error})

    def test_existing_receive_only_check_is_preserved(self):
        self.assertTrue(self.probe(read_error=EOFError("closed"), attempt=False))
        self.assertEqual(self.calls, ["read"])
        self.assertFalse(self.probe(reply={"kind": "confirmed"}, attempt=False))

    def test_concurrent_probe_sends_inside_denial_check(self):
        source = pathlib.Path(__file__).with_name("fixture-inspection-probes.py")
        tree = ast.parse(source.read_text())
        checks = [n.test for n in ast.walk(tree) if isinstance(n, ast.Assert)
                  and isinstance(n.test, ast.Call) and isinstance(n.test.func, ast.Name)
                  and n.test.func.id == "denied" and len(n.test.args) == 2
                  and isinstance(n.test.args[0], ast.Name) and n.test.args[0].id == "other"]
        self.assertEqual(len(checks), 1)


if __name__ == "__main__":
    unittest.main()
