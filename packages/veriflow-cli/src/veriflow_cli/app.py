from __future__ import annotations

import typer

from .commands.discover import app as discover_app
from .commands.run import app as run_app
from .commands.validate import app as validate_app

app = typer.Typer(
    name="veriflow",
    no_args_is_help=True,
    add_completion=False,
    help="Thin CLI adapter over the shared Veriflow spec engine.",
)
app.add_typer(validate_app, name="validate")
app.add_typer(run_app, name="run")
app.add_typer(discover_app, name="discover")
