package render

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	// ContentBinary header value for binary data.
	ContentBinary = "application/octet-stream"
	// ContentHTML header value for HTML data.
	ContentHTML = "text/html"
	// ContentJSON header value for JSON data.
	ContentJSON = "application/json"
	// ContentJSONP header value for JSONP data.
	ContentJSONP = "application/javascript"
	// ContentLength header constant.
	ContentLength = "Content-Length"
	// ContentText header value for Text data.
	ContentText = "text/plain"
	// ContentType header constant.
	ContentType = "Content-Type"
	// ContentXHTML header value for XHTML data.
	ContentXHTML = "application/xhtml+xml"
	// ContentXML header value for XML data.
	ContentXML = "text/xml"
	// Default character encoding.
	defaultCharset = "UTF-8"
)

// Delims represents a set of Left and Right delimiters for HTML template rendering.
type Delims struct {
	// Left delimiter, defaults to {{.
	Left string
	// Right delimiter, defaults to }}.
	Right string
}

// Options is a struct for specifying configuration options for the render.Render object.
type Options struct {
	// Directory to load templates. Default is "".
	Directory string
	// Asset function to use in place of directory. Defaults to nil.
	Asset func(name string) ([]byte, error)
	// AssetNames function to use in place of directory. Defaults to nil.
	AssetNames func() []string
	// Layout template name. Will not render a layout if blank (""). Defaults to blank ("").
	Layout string
	// Extensions to parse template files from. Defaults to [".tmpl"].
	Extensions []string
	// Funcs is a slice of FuncMaps to apply to the template upon compilation. This is useful for helper functions. Defaults to [].
	Funcs []template.FuncMap
	// Delims sets the action delimiters to the specified strings in the Delims struct.
	Delims Delims
	// Appends the given character set to the Content-Type header. Default is "UTF-8".
	Charset string
	// If DisableCharset is set to true, it will not append the above Charset value to the Content-Type header. Default is false.
	DisableCharset bool
	// Outputs human readable JSON.
	IndentJSON bool
	// Outputs human readable XML. Default is false.
	IndentXML bool
	// Prefixes the JSON output with the given bytes. Default is false.
	PrefixJSON []byte
	// Prefixes the XML output with the given bytes.
	PrefixXML []byte
	// Allows changing the binary content type.
	BinaryContentType string
	// Allows changing the HTML content type.
	HTMLContentType string
	// Allows changing the JSON content type.
	JSONContentType string
	// Allows changing the JSONP content type.
	JSONPContentType string
	// Allows changing the Text content type.
	TextContentType string
	// Allows changing the XML content type.
	XMLContentType string
	// If IsDevelopment is set to true, this will recompile the templates on every request. Default is false.
	IsDevelopment bool
	// Unescape HTML characters "&<>" to their original values. Default is false.
	UnEscapeHTML bool
	// Streams JSON responses instead of marshalling prior to sending. Default is false.
	StreamingJSON bool
	// Require that all partials executed in the layout are implemented in all templates using the layout. Default is false.
	RequirePartials bool
	// Disables automatic rendering of http.StatusInternalServerError when an error occurs. Default is false.
	DisableHTTPErrorRendering bool
	// Enables using partials without the current filename suffix which allows use of the same template in multiple files. e.g {{ partial "carosuel" }} inside the home template will match carosel-home or carosel.
	// ***NOTE*** - This option should be named RenderPartialsWithoutSuffix as that is what it does. "Prefix" is a typo. Maintaining the existing name for backwards compatibility.
	RenderPartialsWithoutPrefix bool
}

// HTMLOptions is a struct for overriding some rendering Options for specific HTML call.
type HTMLOptions struct {
	// Layout template name. Overrides Options.Layout.
	Layout string
}

// Render is a service that provides functions for easily writing JSON, XML,
// binary data, and HTML templates out to a HTTP Response.
type Render struct {
	// Customize Secure with an Options struct.
	opt             Options
	templates       *template.Template
	templatesLk     sync.Mutex
	compiledCharset string
}

// New constructs a new Render instance with the supplied options.
func New(options ...Options) *Render {
	var o Options
	if len(options) == 0 {
		o = Options{}
	} else {
		o = options[0]
	}

	r := Render{
		opt: o,
	}

	r.prepareOptions()
	r.compileTemplates()

	return &r
}

func (r *Render) prepareOptions() {
	// Fill in the defaults if need be.
	if len(r.opt.Charset) == 0 {
		r.opt.Charset = defaultCharset
	}
	if r.opt.DisableCharset == false {
		r.compiledCharset = "; charset=" + r.opt.Charset
	}

	if len(r.opt.Extensions) == 0 {
		r.opt.Extensions = []string{".tmpl"}
	}
	if len(r.opt.BinaryContentType) == 0 {
		r.opt.BinaryContentType = ContentBinary
	}
	if len(r.opt.HTMLContentType) == 0 {
		r.opt.HTMLContentType = ContentHTML
	}
	if len(r.opt.JSONContentType) == 0 {
		r.opt.JSONContentType = ContentJSON
	}
	if len(r.opt.JSONPContentType) == 0 {
		r.opt.JSONPContentType = ContentJSONP
	}
	if len(r.opt.TextContentType) == 0 {
		r.opt.TextContentType = ContentText
	}
	if len(r.opt.XMLContentType) == 0 {
		r.opt.XMLContentType = ContentXML
	}
}

func (r *Render) compileTemplates() {
	if r.opt.Asset == nil {
		r.opt.Asset = ioutil.ReadFile
	}
	if r.opt.AssetNames == nil {
		r.opt.AssetNames = func() []string {
			var names []string
			filepath.Walk(r.opt.Directory, func(path string, info os.FileInfo, err error) error {
				// Fix same-extension-dirs bug: some dir might be named to: "users.tmpl", "local.html".
				// These dirs should be excluded as they are not valid golang templates, but files under
				// them should be treat as normal.
				// If is a dir, return immediately (dir is not a valid golang template).
				if info == nil || info.IsDir() {
					return nil
				}
				names = append(names, path)
				return nil
			})
			return names
		}
	}
	r.compileTemplatesFromAsset()
}

func (r *Render) parseTemplate(name string, text string) {
	tmpl := r.templates.New(name)
	template.Must(tmpl.Parse(text))
}

func (r *Render) compileTemplatesFromAsset() {
	dir := r.opt.Directory
	r.templates = template.New(dir)
	r.templates.Delims(r.opt.Delims.Left, r.opt.Delims.Right)
	r.addLayoutFuncs()
	for _, funcs := range r.opt.Funcs {
		r.templates.Funcs(funcs)
	}

	for _, path := range r.opt.AssetNames() {
		if !strings.HasPrefix(path, dir) {
			continue
		}

		rel, err := filepath.Rel(dir, path)
		if err != nil {
			panic(err)
		}

		ext := ""
		if strings.Index(rel, ".") != -1 {
			ext = filepath.Ext(rel)
		}

		for _, extension := range r.opt.Extensions {
			if ext == extension {

				buf, err := r.opt.Asset(path)
				if err != nil {
					panic(err)
				}

				name := rel[0 : len(rel)-len(ext)]

				r.parseTemplate(filepath.ToSlash(name), string(buf))
				break
			}
		}
	}
}

// TemplateLookup is a wrapper around template.Lookup and returns
// the template with the given name that is associated with t, or nil
// if there is no such template.
func (r *Render) TemplateLookup(t string) *template.Template {
	return r.templates.Lookup(t)
}

func (r *Render) execute(name string, binding interface{}) (*bytes.Buffer, error) {
	buf := new(bytes.Buffer)
	return buf, r.templates.ExecuteTemplate(buf, name, binding)
}

func (r *Render) addLayoutFuncs() {
	r.templates.Funcs(template.FuncMap{
		"yield": func(ctx TemplateContext) (template.HTML, error) {
			buf, err := r.execute(ctx.Name, ctx)
			// Return safe HTML here since we are rendering our own template.
			return template.HTML(buf.String()), err
		},
		"partial": func(ctx TemplateContext, partialName string) (template.HTML, error) {
			fullPartialName := fmt.Sprintf("%s-%s", partialName, ctx.Name)
			if r.TemplateLookup(fullPartialName) == nil && r.opt.RenderPartialsWithoutPrefix {
				fullPartialName = partialName
			}
			if r.opt.RequirePartials || r.TemplateLookup(fullPartialName) != nil {
				buf, err := r.execute(fullPartialName, ctx)
				// Return safe HTML here since we are rendering our own template.
				return template.HTML(buf.String()), err
			}
			return "", nil
		},
	})
}

func (r *Render) prepareHTMLOptions(htmlOpt []HTMLOptions) HTMLOptions {
	if len(htmlOpt) > 0 {
		return htmlOpt[0]
	}

	return HTMLOptions{
		Layout: r.opt.Layout,
	}
}

// Render is the generic function called by XML, JSON, Data, HTML, and can be called by custom implementations.
func (r *Render) Render(w io.Writer, e Engine, data interface{}) error {
	err := e.Render(w, data)
	if hw, ok := w.(http.ResponseWriter); err != nil && !r.opt.DisableHTTPErrorRendering && ok {
		http.Error(hw, err.Error(), http.StatusInternalServerError)
	}
	return err
}

// Data writes out the raw bytes as binary data.
func (r *Render) Data(w io.Writer, status int, v []byte) error {
	head := Head{
		ContentType: r.opt.BinaryContentType,
		Status:      status,
	}

	d := Data{
		Head: head,
	}

	return r.Render(w, d, v)
}

// HTML builds up the response from the specified template and bindings.
func (r *Render) HTML(ctx context.Context, w io.Writer, status int, name string, binding interface{}, htmlOpt ...HTMLOptions) error {
	// If we are in development mode, recompile the templates on every HTML request.
	if r.opt.IsDevelopment {
		r.templatesLk.Lock()
		defer r.templatesLk.Unlock()
		r.compileTemplates()
	}

	head := Head{
		ContentType: r.opt.HTMLContentType + r.compiledCharset,
		Status:      status,
	}

	h := HTML{
		Head:      head,
		Name:      name,
		Layout:    r.prepareHTMLOptions(htmlOpt).Layout,
		Ctx:       ctx,
		Templates: r.templates,
	}

	return r.Render(w, h, binding)
}

// JSON marshals the given interface object and writes the JSON response.
func (r *Render) JSON(w io.Writer, status int, v interface{}) error {
	head := Head{
		ContentType: r.opt.JSONContentType + r.compiledCharset,
		Status:      status,
	}

	j := JSON{
		Head:          head,
		Indent:        r.opt.IndentJSON,
		Prefix:        r.opt.PrefixJSON,
		UnEscapeHTML:  r.opt.UnEscapeHTML,
		StreamingJSON: r.opt.StreamingJSON,
	}

	return r.Render(w, j, v)
}

// JSONP marshals the given interface object and writes the JSON response.
func (r *Render) JSONP(w io.Writer, status int, callback string, v interface{}) error {
	head := Head{
		ContentType: r.opt.JSONPContentType + r.compiledCharset,
		Status:      status,
	}

	j := JSONP{
		Head:     head,
		Indent:   r.opt.IndentJSON,
		Callback: callback,
	}

	return r.Render(w, j, v)
}

// Text writes out a string as plain text.
func (r *Render) Text(w io.Writer, status int, v string) error {
	head := Head{
		ContentType: r.opt.TextContentType + r.compiledCharset,
		Status:      status,
	}

	t := Text{
		Head: head,
	}

	return r.Render(w, t, v)
}

// XML marshals the given interface object and writes the XML response.
func (r *Render) XML(w io.Writer, status int, v interface{}) error {
	head := Head{
		ContentType: r.opt.XMLContentType + r.compiledCharset,
		Status:      status,
	}

	x := XML{
		Head:   head,
		Indent: r.opt.IndentXML,
		Prefix: r.opt.PrefixXML,
	}

	return r.Render(w, x, v)
}
