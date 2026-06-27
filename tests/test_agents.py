import unittest
from unittest.mock import patch
from middle_manager.agents import autodetect_step_agents

class TestAgents(unittest.TestCase):

    @patch('middle_manager.agents.agent_available')
    def test_autodetect_step_agents_diverse(self, mock_available):
        # Mock only 'grok', 'crush', 'agy' as installed
        mock_available.side_effect = lambda name, override=None: name in ('grok', 'crush', 'agy')

        detected = autodetect_step_agents()
        self.assertEqual(detected['discover'], 'grok')
        self.assertEqual(detected['execute'], 'crush')
        self.assertEqual(detected['verify'], 'agy')
        self.assertEqual(detected['commit'], 'agy')

    def test_get_process_tree_cpu_ticks(self):
        import os
        from middle_manager.agents import get_process_tree_cpu_ticks
        ticks = get_process_tree_cpu_ticks(os.getpid())
        self.assertIsNotNone(ticks)
        self.assertGreaterEqual(ticks, 0)

    def test_calculate_cpu_percent(self):
        import os
        import time
        from middle_manager.agents import calculate_cpu_percent
        pid = os.getpid()
        # initial sample
        cpu, last_ticks, last_time = calculate_cpu_percent(pid, None, time.time() - 1.0)
        self.assertEqual(cpu, 0.0)
        self.assertIsNotNone(last_ticks)
        
        # secondary sample
        cpu2, last_ticks2, last_time2 = calculate_cpu_percent(pid, last_ticks, last_time)
        self.assertIsNotNone(cpu2)

    def test_read_available(self):
        import io
        from middle_manager.agents import read_available
        stream = io.StringIO("hello world")
        self.assertEqual(read_available(stream), "hello world")

    def test_run_agent_monitored_dry(self):
        from pathlib import Path
        from middle_manager.agents import run_agent, AgentRun
        run = AgentRun(
            agent="grok",
            command=["echo", "hello"],
            prompt="hello",
            cwd=Path("/tmp")
        )
        res = run_agent(run, dry_run=True, stream=False)
        self.assertEqual(res.returncode, 0)

    def test_run_agent_monitored_real(self):
        from pathlib import Path
        from middle_manager.agents import run_agent, AgentRun
        import shutil
        echo_bin = shutil.which("echo")
        if echo_bin:
            run = AgentRun(
                agent="grok",
                command=[echo_bin, "hello"],
                prompt="hello",
                cwd=Path("/tmp")
            )
            res = run_agent(run, dry_run=False, stream=False)
            self.assertEqual(res.returncode, 0)
            self.assertEqual(res.stdout.strip(), "hello")
