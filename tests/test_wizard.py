import unittest
from unittest.mock import patch
from pathlib import Path
from middle_manager.wizard import _prompt, _choose, _yes_no, run_wizard
from middle_manager.config import LoopConfig

class TestWizard(unittest.TestCase):

    @patch('builtins.input', side_effect=['custom_val', ''])
    def test_prompt(self, mock_input):
        val1 = _prompt("Enter value", default="default_val")
        self.assertEqual(val1, 'custom_val')
        
        val2 = _prompt("Enter value", default="default_val")
        self.assertEqual(val2, 'default_val')

    @patch('builtins.input', side_effect=['invalid', 'feature'])
    @patch('builtins.print')
    def test_choose(self, mock_print, mock_input):
        options = [('feature', 'Build a feature'), ('repair', 'Repair')]
        val = _choose("Pick option", options, default_key='repair')
        self.assertEqual(val, 'feature')

    @patch('builtins.input', side_effect=['', 'y', 'n', 'yes', 'no'])
    def test_yes_no(self, mock_input):
        self.assertTrue(_yes_no("Proceed", default=True))
        self.assertTrue(_yes_no("Proceed", default=False))  # 'y' input
        self.assertFalse(_yes_no("Proceed", default=True))  # 'n' input
        self.assertTrue(_yes_no("Proceed", default=False))  # 'yes' input
        self.assertFalse(_yes_no("Proceed", default=True))  # 'no' input

    @patch('middle_manager.wizard._tty', return_value=True)
    @patch('middle_manager.wizard._prompt', side_effect=['/tmp', 'some mission', 'y', '10'])
    @patch('middle_manager.wizard._choose', return_value='feature')
    @patch('middle_manager.wizard._yes_no', side_effect=[False, True, False, False, True, True, True]) # customize agents, yolo, dry_run, pause, fix unrelated, open prs, start loop
    @patch('middle_manager.wizard.load_last_config', return_value={})
    @patch('middle_manager.wizard.save_last_config')
    @patch('middle_manager.wizard.repo_is_git', return_value=True)
    @patch('pathlib.Path.exists', return_value=True)
    def test_run_wizard_success(self, mock_exists, mock_git, mock_save, mock_load, mock_yes_no, mock_choose, mock_prompt, mock_tty):
        cfg = run_wizard()
        self.assertIsNotNone(cfg)
        self.assertEqual(str(cfg.repo), '/tmp')
        self.assertEqual(cfg.mode, 'feature')
        self.assertEqual(cfg.mission, 'some mission')
        self.assertTrue(cfg.yolo)
        self.assertFalse(cfg.dry_run)

    @patch('middle_manager.wizard._tty', return_value=True)
    @patch('middle_manager.wizard._prompt', side_effect=['', 'some mission', 'y', '10'])
    @patch('middle_manager.wizard._choose', return_value='feature')
    @patch('middle_manager.wizard._yes_no', side_effect=[False, True, False, False, True, True, True])
    @patch('middle_manager.wizard.load_last_config', return_value={'repo': '/some/old/path'})
    @patch('middle_manager.wizard.save_last_config')
    @patch('middle_manager.wizard.repo_is_git', return_value=True)
    @patch('pathlib.Path.exists', return_value=True)
    def test_run_wizard_defaults_to_cwd(self, mock_exists, mock_git, mock_save, mock_load, mock_yes_no, mock_choose, mock_prompt, mock_tty):
        import os
        cfg = run_wizard()
        self.assertIsNotNone(cfg)
        self.assertEqual(str(cfg.repo), os.getcwd())
