import unittest
from unittest.mock import patch
from middle_manager.agents import autodetect_step_agents, get_acp_command

class TestAgents(unittest.TestCase):

    @patch('middle_manager.agents.agent_available')
    def test_autodetect_step_agents_diverse(self, mock_available):
        # Mock only 'grok', 'opencode', 'agy' as installed
        mock_available.side_effect = lambda name, override=None: name in ('grok', 'opencode', 'agy')

        detected = autodetect_step_agents()
        self.assertEqual(detected['discover'], 'grok')
        self.assertEqual(detected['execute'], 'opencode')
        self.assertEqual(detected['verify'], 'agy')
        self.assertEqual(detected['commit'], 'agy')

    def test_get_acp_command(self):
        cmd = get_acp_command("grok")
        self.assertEqual(cmd[1:], ["agent", "stdio"])

        cmd_op = get_acp_command("opencode")
        self.assertEqual(cmd_op[1:], ["acp"])

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

    def test_loop_task_auto_checkoff_logic(self):
        from middle_manager.config import LoopConfig
        from middle_manager.loop import MiddleManagerLoop
        import tempfile
        from pathlib import Path
        
        with tempfile.TemporaryDirectory() as tmpdir:
            cfg = LoopConfig(repo=Path(tmpdir))
            loop = MiddleManagerLoop(cfg)
            
            # Setup plan
            plan_content = (
                "# fix_plan.md\n\n"
                "## Tasks\n"
                "- [ ] Task 1\n"
                "- [ ] Task 2\n"
            )
            loop.write_text(loop.fix_plan_path, plan_content)
            
            # Scenario 1: tasks_before is None (e.g. step execute was skipped)
            # It should fall back to checking off the top item (Task 1)
            tasks_before = None
            if tasks_before is not None:
                tasks_after = len([line for line in loop.read_text(loop.fix_plan_path).splitlines() if line.strip().startswith("- [ ]")])
                if tasks_after >= tasks_before:
                    loop._check_off_top_items(cfg.batch_size)
            else:
                loop._check_off_top_items(cfg.batch_size)
            
            updated = loop.read_text(loop.fix_plan_path)
            self.assertIn("- [x] Task 1", updated)
            self.assertIn("- [ ] Task 2", updated)
            
            # Scenario 2: agent did NOT check off any task (tasks_after >= tasks_before)
            # Setup plan again
            loop.write_text(loop.fix_plan_path, plan_content)
            tasks_before = 2
            tasks_after = 2
            if tasks_after >= tasks_before:
                loop._check_off_top_items(cfg.batch_size)
            
            updated = loop.read_text(loop.fix_plan_path)
            self.assertIn("- [x] Task 1", updated)
            self.assertIn("- [ ] Task 2", updated)
            
            # Scenario 3: agent DID check off a task (tasks_after < tasks_before)
            # Setup plan with Task 1 already checked off by the agent
            agent_modified_plan = (
                "# fix_plan.md\n\n"
                "## Tasks\n"
                "- [x] Task 1\n"
                "- [ ] Task 2\n"
            )
            loop.write_text(loop.fix_plan_path, agent_modified_plan)
            tasks_before = 2
            tasks_after = 1 # Task 1 is checked off, so only 1 remains unchecked
            if tasks_after >= tasks_before:
                loop._check_off_top_items(cfg.batch_size)
                
            updated = loop.read_text(loop.fix_plan_path)
            self.assertIn("- [x] Task 1", updated)
            self.assertIn("- [ ] Task 2", updated) # Task 2 remains unchecked!

    def test_build_command_interactive(self):
        from pathlib import Path
        from middle_manager.agents import build_command
        
        # Test grok in interactive mode
        run_grok = build_command(
            "grok",
            "test prompt",
            cwd=Path("/tmp"),
            interactive=True,
            prompt_file=Path("/tmp/prompt.md")
        )
        self.assertNotIn("--prompt-file", run_grok.command)
        self.assertIn("test prompt", run_grok.command)
        self.assertNotIn("-p", run_grok.command)
        
        # Test claude in interactive mode
        run_claude = build_command(
            "claude",
            "test prompt",
            cwd=Path("/tmp"),
            interactive=True
        )
        self.assertIn("test prompt", run_claude.command)
        self.assertNotIn("-p", run_claude.command)

    def test_build_interactive_command(self):
        from pathlib import Path
        from middle_manager.config import LoopConfig
        from middle_manager.loop import MiddleManagerLoop
        
        cfg = LoopConfig(repo=Path("/tmp"))
        loop = MiddleManagerLoop(cfg)
        
        grok_cmd = loop._build_interactive_command("grok", "test prompt")
        self.assertIn("grok", grok_cmd)
        self.assertNotIn("-p", grok_cmd)
        self.assertIn('"test prompt"', grok_cmd)
        
        claude_cmd = loop._build_interactive_command("claude", "test prompt")
        self.assertNotIn("-p", claude_cmd)
        self.assertIn('"test prompt"', claude_cmd)
        
        agy_cmd = loop._build_interactive_command("agy", "test prompt")
        self.assertNotIn("--print", agy_cmd)
        self.assertIn("--prompt-interactive", agy_cmd)
