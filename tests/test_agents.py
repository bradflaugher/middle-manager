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

    def test_run_command_monitored_string(self):
        from pathlib import Path
        from middle_manager.agents import run_command_monitored
        import shutil
        echo_bin = shutil.which("echo")
        if echo_bin:
            res = run_command_monitored(
                command=f"{echo_bin} hello_from_shell",
                cwd=Path("/tmp"),
                stream=False,
                label="TEST SHELL",
            )
            self.assertEqual(res.returncode, 0)
            self.assertEqual(res.stdout.strip(), "hello_from_shell")

    def test_ensure_gitignore(self):
        import tempfile
        from pathlib import Path
        from middle_manager.loop import MiddleManagerLoop
        from middle_manager.config import LoopConfig

        with tempfile.TemporaryDirectory() as tmpdir:
            tmppath = Path(tmpdir)
            # Create a mock .git directory to satisfy repo_is_git
            (tmppath / ".git").mkdir()
            
            cfg = LoopConfig(repo=tmppath)
            loop = MiddleManagerLoop(cfg)
            
            # Case 1: .gitignore doesn't exist
            loop.ensure_gitignore()
            gitignore_path = tmppath / ".gitignore"
            self.assertTrue(gitignore_path.exists())
            self.assertIn(".middle-manager/", gitignore_path.read_text())
            
            # Case 2: .gitignore exists but does not ignore .middle-manager/
            gitignore_path.write_text("node_modules/\n")
            loop.ensure_gitignore()
            self.assertIn(".middle-manager/", gitignore_path.read_text())
            self.assertIn("node_modules/", gitignore_path.read_text())
            
            # Case 3: .gitignore already ignores .middle-manager/ (no duplicate added)
            content_before = gitignore_path.read_text()
            loop.ensure_gitignore()
            content_after = gitignore_path.read_text()
            self.assertEqual(content_before, content_after)

    def test_top_plan_item(self):
        import tempfile
        from pathlib import Path
        from middle_manager.loop import MiddleManagerLoop
        from middle_manager.config import LoopConfig

        with tempfile.TemporaryDirectory() as tmpdir:
            tmppath = Path(tmpdir)
            cfg = LoopConfig(repo=tmppath)
            loop = MiddleManagerLoop(cfg)
            
            # Case 1: normal plan with notes first, then tasks
            plan_content = (
                "# fix_plan.md\n\n"
                "## Notes\n\n"
                "- **Test command:** pytest\n"
                "- **Placement:** root\n\n"
                "## Tasks\n\n"
                "- [x] Done task\n"
                "- [ ] Actual top task\n"
                "- [ ] Another pending task\n"
            )
            loop.write_text(loop.fix_plan_path, plan_content)
            self.assertEqual(loop.top_plan_item(), "Actual top task")

            # Case 2: no checkboxed tasks, fallback to loose tasks under ## Tasks section
            plan_content = (
                "# fix_plan.md\n\n"
                "## Notes\n"
                "- Not a task\n\n"
                "## Tasks\n"
                "- Loose task\n"
            )
            loop.write_text(loop.fix_plan_path, plan_content)
            self.assertEqual(loop.top_plan_item(), "Loose task")

            # Case 3: no tasks at all
            plan_content = (
                "# fix_plan.md\n\n"
                "## Notes\n"
                "- Just a note\n"
            )
            loop.write_text(loop.fix_plan_path, plan_content)
            self.assertEqual(
                loop.top_plan_item(),
                "No actionable item in fix_plan.md — add `- [ ] task` lines."
            )

            # Test top_plan_items batching
            plan_content = (
                "# fix_plan.md\n\n"
                "## Tasks\n"
                "- [ ] Task 1\n"
                "- [ ] Task 2\n"
                "- [ ] Task 3\n"
            )
            loop.write_text(loop.fix_plan_path, plan_content)
            self.assertEqual(loop.top_plan_items(2), ["Task 1", "Task 2"])
            self.assertEqual(loop.top_plan_items(5), ["Task 1", "Task 2", "Task 3"])

            # Test _check_off_top_items
            loop._check_off_top_items(2)
            updated_text = loop.read_text(loop.fix_plan_path)
            self.assertIn("- [x] Task 1", updated_text)
            self.assertIn("- [x] Task 2", updated_text)
            self.assertIn("- [ ] Task 3", updated_text)


    def test_run_command_monitored_tmux(self):
        import shutil
        from pathlib import Path
        from middle_manager.agents import run_command_monitored
        
        if shutil.which("tmux"):
            res = run_command_monitored(
                command="echo hello_from_tmux",
                cwd=Path("/tmp"),
                stream=False,
                label="TEST TMUX",
                tmux=True,
                tmux_session="mm-test-session"
            )
            self.assertEqual(res.returncode, 0)
            self.assertIn("hello_from_tmux", res.stdout)


