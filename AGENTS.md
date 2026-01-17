* Brevity is good, don't be clever.
* When doing copy-edits, try to stay as close to the style of the origin as possible.
* Assume that the maintainers and readers of the code you write are Go experts:
   * Don't use comments to explain the obvious.
   * Use self-explanatory variable and function names.
   * Use short variable names when the context is clear.
* Never export symbols that's not needed outside of the package.
* Avoid global state at (almost) all cost.