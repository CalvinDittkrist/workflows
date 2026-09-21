module github.com/CalvinDittkrist/workflows/factory

go 1.25

// The dashboard is an npm package inside this one, and npm packages ship Go files of their own:
// without this the go command would build and test what node_modules happens to contain.
ignore ./ui/node_modules
