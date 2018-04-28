package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/geek1011/kobopatch/patchlib"
	yaml "gopkg.in/yaml.v2"
)

var version = "unknown"

type config struct {
	Version           string            `yaml:"version" json:"version"`
	In                string            `yaml:"in" json:"in"`
	Out               string            `yaml:"out" json:"out"`
	Log               string            `yaml:"log" json:"log"`
	UseNewPatchFormat bool              `yaml:"useNewPatchFormat" json:"useNewPatchFormat"`
	Patches           map[string]string `yaml:"patches" json:"patches"`
}

var log = func(format string, a ...interface{}) {}

func main() {
	fmt.Printf("kobopatch %s\n\n", version)

	cfgbuf, err := ioutil.ReadFile("./kobopatch.yaml")
	checkErr(err, "Could not read kobopatch.yaml")

	cfg := &config{}
	err = yaml.UnmarshalStrict(cfgbuf, &cfg)
	checkErr(err, "Could not parse kobopatch.yaml")

	if cfg.Version == "" || cfg.In == "" || cfg.Out == "" || cfg.Log == "" {
		checkErr(errors.New("version, in, out, and log are required"), "Could not parse kobopatch.yaml")
	}

	if !cfg.UseNewPatchFormat {
		checkErr(errors.New("only the new patch format is supported"), "Error")
	}

	logf, err := os.Create(cfg.Log)
	checkErr(err, "Could not open and truncate log file")
	defer logf.Close()

	log = func(format string, a ...interface{}) {
		fmt.Fprintf(logf, format, a...)
	}

	d, _ := os.Getwd()
	log("kobopatch %s\n\ndir:%s\ncfg: %#v\n\n", version, d, cfg)

	log("opening zip\n")
	zipr, err := zip.OpenReader(cfg.In)
	checkErr(err, "Could not open input file")
	defer zipr.Close()

	log("searching for KoboRoot.tgz\n")
	var tgzr io.ReadCloser
	for _, f := range zipr.File {
		log("  file: %s\n", f.Name)
		if f.Name == "KoboRoot.tgz" {
			log("found KoboRoot.tgz, opening\n")
			tgzr, err = f.Open()
			checkErr(err, "Could not open KoboRoot.tgz")
			break
		}
	}
	if tgzr == nil {
		log("KoboRoot.tgz reader empty so KoboRoot.tgz not in zip\n")
		checkErr(errors.New("no such file in zip"), "Could not open KoboRoot.tgz")
	}
	defer tgzr.Close()

	log("creating new gzip reader for tgz\n")
	tdr, err := gzip.NewReader(tgzr)
	checkErr(err, "Could not decompress KoboRoot.tgz")
	defer tdr.Close()

	log("creating new tar reader for gzip reader for tgz\n")
	tr := tar.NewReader(tdr)
	checkErr(err, "Could not read KoboRoot.tgz as tar archive")

	log("creating new buffer for output\n")
	var outw bytes.Buffer
	outzw := gzip.NewWriter(&outw)
	defer outzw.Close()

	log("creating new tar writer for output buffer\n")
	outtw := tar.NewWriter(outzw)
	defer outtw.Close()

	log("looping over files from source tgz\n")
	for {
		log("  reading entry\n")
		h, err := tr.Next()
		if err == io.EOF {
			err = nil
			break
		}
		checkErr(err, "Could not read entry from KoboRoot.tgz")
		log("    entry: %s - size:%d, mode:%v\n", h.Name, h.Size, h.Mode)

		log("    checking if entry needs patching\n")
		var needsPatching bool
		var pfn string
		for n, f := range cfg.Patches {
			if h.Name == "./"+f || h.Name == f {
				log("    entry needs patching\n")
				needsPatching = true
				pfn = n
				break
			}
		}

		if !needsPatching {
			log("    entry does not need patching\n")
			continue
		}

		log("    checking type before patching - typeflag: %v\n", h.Typeflag)
		fmt.Printf("Patching %s\n", h.Name)

		if h.Typeflag != tar.TypeReg {
			checkErr(errors.New("not a regular file"), "Could not patch file")
		}

		log("    reading entry contents\n")
		fbuf, err := ioutil.ReadAll(tr)
		checkErr(err, "Could not read file contents from KoboRoot.tgz")

		pt := patchlib.NewPatcher(fbuf)

		log("    loading patch file: %s\n", pfn)
		pf, err := newPatchFile(pfn)
		checkErr(err, "Could not read and parse patch file "+pfn)

		log("    applying patch file\n")
		err = pf.ApplyTo(pt)
		checkErr(err, "Could not apply patch file "+pfn)

		fbuf = pt.GetBytes()

		log("    copying new header to output tar - size:%d, mode:%v\n", len(fbuf), h.Mode)
		// Preserve attributes (VERY IMPORTANT)
		err = outtw.WriteHeader(&tar.Header{
			Typeflag:   h.Typeflag,
			Name:       h.Name,
			Mode:       h.Mode,
			Uid:        h.Uid,
			Gid:        h.Gid,
			ModTime:    time.Now(),
			Uname:      h.Uname,
			Gname:      h.Gname,
			PAXRecords: h.PAXRecords,
			Size:       int64(len(fbuf)),
			Format:     h.Format,
		})
		checkErr(err, "Could not write new header to patched KoboRoot.tgz")

		log("    writing patched binary to output\n")
		i, err := outtw.Write(fbuf)
		checkErr(err, "Could not write new file to patched KoboRoot.tgz")
		if i != len(fbuf) {
			checkErr(errors.New("could not write whole file"), "Could not write new file to patched KoboRoot.tgz")
		}
	}

	log("removing old output tgz: %s\n", cfg.Out)
	os.Remove(cfg.Out)

	log("flushing output tar writer to buffer\n")
	err = outtw.Close()
	checkErr(err, "Could not finish writing patched tar")
	time.Sleep(time.Millisecond * 500)

	log("flushing output gzip writer to buffer\n")
	err = outzw.Close()
	checkErr(err, "Could not finish writing compressed patched tar")
	time.Sleep(time.Millisecond * 500)

	log("writing buffer to output file\n")
	err = ioutil.WriteFile(cfg.Out, outw.Bytes(), 0644)
	checkErr(err, "Could not write patched KoboRoot.tgz")

	log("patch success\n")
	fmt.Printf("Successfully saved patched KoboRoot.tgz to %s\n", cfg.Out)

	if runtime.GOOS == "windows" {
		fmt.Printf("\n\nWaiting 60 seconds because runnning on Windows\n")
		time.Sleep(time.Second * 60)
	}
}

func checkErr(err error, msg string) {
	if err == nil {
		return
	}
	if msg != "" {
		log("Fatal: %s: %v\n", msg, err)
		fmt.Fprintf(os.Stderr, "Fatal: %s: %v\n", msg, err)
	} else {
		log("Fatal: %v\n", err)
		fmt.Fprintf(os.Stderr, "Fatal: %v\n", err)
	}
	if runtime.GOOS == "windows" {
		fmt.Printf("\n\nWaiting 60 seconds because runnning on Windows\n")
		time.Sleep(time.Second * 60)
	}
	os.Exit(1)
}

type patchFile map[string]patch
type patch []instruction
type instruction struct {
	Enabled               *bool   `yaml:"Enabled,omitempty"`
	Description           *string `yaml:"Description,omitempty"`
	PatchGroup            *string `yaml:"PatchGroup,omitempty"`
	BaseAddress           *int32  `yaml:"BaseAddress,omitempty"`
	FindBaseAddressHex    *string `yaml:"FindBaseAddressHex,omitempty"`
	FindBaseAddressString *string `yaml:"FindBaseAddressString,omitempty"`
	FindReplaceString     *struct {
		Find    string `yaml:"Find,omitempty"`
		Replace string `yaml:"Replace,omitempty"`
	} `yaml:"FindReplaceString,omitempty"`
	ReplaceString *struct {
		Offset  int32  `yaml:"Offset,omitempty"`
		Find    string `yaml:"Find,omitempty"`
		Replace string `yaml:"Replace,omitempty"`
	} `yaml:"ReplaceString,omitempty"`
	ReplaceInt *struct {
		Offset  int32 `yaml:"Offset,omitempty"`
		Find    uint8 `yaml:"Find,omitempty"`
		Replace uint8 `yaml:"Replace,omitempty"`
	} `yaml:"ReplaceInt,omitempty"`
	ReplaceFloat *struct {
		Offset  int32   `yaml:"Offset,omitempty"`
		Find    float64 `yaml:"Find,omitempty"`
		Replace float64 `yaml:"Replace,omitempty"`
	} `yaml:"ReplaceFloat,omitempty"`
	ReplaceBytes *struct {
		Offset   int32   `yaml:"Offset,omitempty"`
		FindH    *string `yaml:"FindH,omitempty"`
		ReplaceH *string `yaml:"ReplaceH,omitempty"`
		Find     []byte  `yaml:"Find,omitempty"`
		Replace  []byte  `yaml:"Replace,omitempty"`
	} `yaml:"ReplaceBytes,omitempty"`
}

func newPatchFile(filename string) (*patchFile, error) {
	log("        loading patch file\n")
	buf, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("error reading patch file: %v", err)
	}

	log("        parsing patch file\n")
	pf := &patchFile{}
	err = yaml.UnmarshalStrict(buf, &pf)
	if err != nil {
		return nil, fmt.Errorf("error parsing patch file: %v", err)
	}

	log("        parsing patch file: expanding shorthand hex values\n")
	for n := range *pf {
		for i := range (*pf)[n] {
			if (*pf)[n][i].ReplaceBytes != nil {
				if ((*pf)[n][i].ReplaceBytes).FindH != nil {
					hex := *((*pf)[n][i].ReplaceBytes).FindH
					_, err := fmt.Sscanf(
						strings.Replace(hex, " ", "", -1),
						"%x\n",
						&((*pf)[n][i].ReplaceBytes).Find,
					)
					if err != nil {
						log("          error decoding hex `%s`: %v\n", hex, err)
						return nil, fmt.Errorf("error parsing patch file: error expanding shorthand hex `%s`", hex)
					}
					log("          decoded hex `%s` to `%v`\n", hex, ((*pf)[n][i].ReplaceBytes).Find)
				}
				if ((*pf)[n][i].ReplaceBytes).ReplaceH != nil {
					hex := *((*pf)[n][i].ReplaceBytes).ReplaceH
					_, err := fmt.Sscanf(
						strings.Replace(hex, " ", "", -1),
						"%x\n",
						&((*pf)[n][i].ReplaceBytes).Replace,
					)
					if err != nil {
						log("          error decoding hex `%s`: %v\n", hex, err)
						return nil, fmt.Errorf("error parsing patch file: error expanding shorthand hex `%s`", hex)
					}
					log("          decoded hex `%s` to `%v`\n", hex, ((*pf)[n][i].ReplaceBytes).Replace)
				}
			}
		}
	}

	log("        validating patch file\n")
	err = pf.validate()
	if err != nil {
		return nil, fmt.Errorf("invalid patch file: %v", err)
	}

	return pf, nil
}

func (pf *patchFile) ApplyTo(pt *patchlib.Patcher) error {
	log("        validating patch file\n")
	err := pf.validate()
	if err != nil {
		err = fmt.Errorf("invalid patch file: %v", err)
		fmt.Printf("  Error: %v\n", err)
		return err
	}

	log("        looping over patches\n")
	num, total := 0, len(*pf)
	for n, p := range *pf {
		var err error
		num++
		log("          ResetBaseAddress()\n")
		pt.ResetBaseAddress()

		enabled := false
		for _, i := range p {
			if i.Enabled != nil && *i.Enabled {
				enabled = *i.Enabled
				break
			}
		}
		log("          Enabled: %t\n", enabled)

		if !enabled {
			log("          skipping patch `%s`\n", n)
			fmt.Printf("  [%d/%d] Skipping disabled patch `%s`\n", num, total, n)
			continue
		}

		log("          applying patch `%s`\n", n)
		fmt.Printf("  [%d/%d] Applying patch `%s`\n", num, total, n)

		log("        looping over instructions\n")
		for _, i := range p {
			switch {
			case i.Enabled != nil || i.PatchGroup != nil || i.Description != nil:
				log("          skipping non-instruction Enabled(), PatchGroup() or Description()\n")
				// Skip non-instructions
				err = nil
			case i.BaseAddress != nil:
				log("          BaseAddress(%#v)\n", *i.BaseAddress)
				err = pt.BaseAddress(*i.BaseAddress)
			case i.FindBaseAddressHex != nil:
				log("          FindBaseAddressHex(%#v)\n", *i.FindBaseAddressHex)
				buf := []byte{}
				_, err = fmt.Sscanf(strings.Replace(*i.FindBaseAddressHex, " ", "", -1), "%x\n", &buf)
				if err != nil {
					err = fmt.Errorf("FindBaseAddresHex: invalid hex string")
					break
				}
				err = pt.FindBaseAddress(buf)
			case i.FindBaseAddressString != nil:
				log("          FindBaseAddressString(%#v) | hex:%x\n", *i.FindBaseAddressString, []byte(*i.FindBaseAddressString))
				err = pt.FindBaseAddressString(*i.FindBaseAddressString)
			case i.ReplaceBytes != nil:
				r := *i.ReplaceBytes
				log("          ReplaceBytes(%#v, %#v, %#v)\n", r.Offset, r.Find, r.Replace)
				err = pt.ReplaceBytes(r.Offset, r.Find, r.Replace)
			case i.ReplaceFloat != nil:
				r := *i.ReplaceFloat
				log("          ReplaceFloat(%#v, %#v, %#v)\n", r.Offset, r.Find, r.Replace)
				err = pt.ReplaceFloat(r.Offset, r.Find, r.Replace)
			case i.ReplaceInt != nil:
				r := *i.ReplaceInt
				log("          ReplaceInt(%#v, %#v, %#v)\n", r.Offset, r.Find, r.Replace)
				err = pt.ReplaceInt(r.Offset, r.Find, r.Replace)
			case i.ReplaceString != nil:
				r := *i.ReplaceString
				log("          ReplaceString(%#v, %#v, %#v)\n", r.Offset, r.Find, r.Replace)
				err = pt.ReplaceString(r.Offset, r.Find, r.Replace)
			case i.FindReplaceString != nil:
				r := *i.FindReplaceString
				log("          FindReplaceString(%#v, %#v)\n", r.Find, r.Replace)
				log("            FindBaseAddressString(%#v)\n", r.Find)
				err = pt.FindBaseAddressString(r.Find)
				if err != nil {
					err = fmt.Errorf("FindReplaceString: %v", err)
					break
				}
				log("            ReplaceString(0, %#v, %#v)\n", r.Find, r.Replace)
				err = pt.ReplaceString(0, r.Find, r.Replace)
				if err != nil {
					err = fmt.Errorf("FindReplaceString: %v", err)
					break
				}
			default:
				log("          invalid instruction: %#v\n", i)
				err = fmt.Errorf("invalid instruction: %#v", i)
			}

			if err != nil {
				log("        could not apply patch: %v\n", err)
				fmt.Printf("    Error: could not apply patch: %v\n", err)
				return err
			}
		}
	}
	return nil
}

func (pf *patchFile) validate() error {
	enabledPatchGroups := map[string]bool{}
	for n, p := range *pf {
		ec := 0
		e := false
		pgc := 0
		pg := ""
		dc := 0

		rbc := 0
		roc := 0
		fbsc := 0

		for _, i := range p {
			ic := 0
			if i.Enabled != nil {
				ec++
				e = *i.Enabled
				ic++
			}
			if i.Description != nil {
				dc++
				ic++
			}
			if i.PatchGroup != nil {
				pgc++
				pg = *i.PatchGroup
				ic++
			}
			if i.BaseAddress != nil {
				ic++
			}
			if i.FindBaseAddressString != nil {
				ic++
				fbsc++
			}
			if i.FindBaseAddressHex != nil {
				ic++
			}
			if i.ReplaceBytes != nil {
				ic++
				rbc++
			}
			if i.ReplaceFloat != nil {
				ic++
				roc++
			}
			if i.ReplaceInt != nil {
				ic++
				roc++
			}
			if i.ReplaceString != nil {
				ic++
				roc++
			}
			if i.FindReplaceString != nil {
				ic++
				roc++
			}
			log("          ic:%d\n", ic)
			if ic < 1 {
				return fmt.Errorf("internal error while validating `%s` (you should report this as a bug)", n)
			}
			if ic > 1 {
				return fmt.Errorf("more than one instruction per bullet in patch `%s` (you might be missing a -)", n)
			}
		}
		log("          ec:%d, e:%t, pgc:%d, pg:%s, dc:%d, rbc:%d, roc: %d, fbsc:%d\n", ec, e, pgc, pg, dc, rbc, roc, fbsc)
		if ec < 1 {
			return fmt.Errorf("no `Enabled` option in `%s`", n)
		} else if ec > 1 {
			return fmt.Errorf("more than one `Enabled` option in `%s`", n)
		}
		if dc > 1 {
			return fmt.Errorf("more than one `Description` option in `%s` (use comments to describe individual lines)", n)
		}
		if pgc > 1 {
			return fmt.Errorf("more than one `PatchGroup` option in `%s`", n)
		}
		if pg != "" && e {
			if _, ok := enabledPatchGroups[pg]; ok {
				return fmt.Errorf("more than one patch enabled in PatchGroup `%s`", pg)
			}
			enabledPatchGroups[pg] = true
		}
		if roc == 0 && rbc > 0 && fbsc > 0 {
			return fmt.Errorf("use FindBaseAddressHex for hex replacements because FindBaseAddressString will lose control characters (patch `%s`)", n)
		}
	}
	log("          enabledPatchGroups:%v\n", enabledPatchGroups)
	return nil
}
