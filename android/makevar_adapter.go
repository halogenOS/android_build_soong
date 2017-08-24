package android

import (
  "fmt"
  "os"
  "strings"
  "bufio"
  "io"
)

const debug bool = false

// List of files to be parsed in order to get global variables into soong etc.
var makevarFiles = []string{
    os.Getenv("ANDROID_BUILD_TOP")+"/"+os.Getenv("TARGET_DEVICE_DIR")+
      "/BoardConfig.mk",
}

// The variables will be stored here. Each entry consists of two strings,
// one being the key (variable name) and the other the value (variable value)
// It has some environment variables already
var makevars = [][]string{
  {"ANDROID_BUILD_TOP", os.Getenv("ANDROID_BUILD_TOP")},
  {"TARGET_DEVICE_DIR", os.Getenv("TARGET_DEVICE_DIR")},
}
var predefinedMakevarsLen int = len(makevars)

// Get a makevar
func GetMakeVar(key string, def string) string {
  if makevars == nil || len(makevars) == predefinedMakevarsLen {
    if InitMakeVars() != 0 {
      panic("Failed to initialize makevars!")
    }
  }
  for i := 0; i < len(makevars); i++ {
      if makevars[i][0] == key {
        return makevars[i][1]
      }
  }
  return def
}

// Read all makevar files and put them in the makevars array
// This will be called when getMakeVar is first called (only if this method
// hasn't been called before)
func InitMakeVars() int {
  for i := 0; i < len(makevarFiles); i++ {
    f, err := os.Open(makevarFiles[i])
    if err != nil {
      panic(fmt.Sprintf("Error opening makevar file ", err))
    }
    defer f.Close()
    r := bufio.NewReader(f)
    for linenum := 0; ; linenum++ {
      line, err := r.ReadString(10) // 0x0A separator = newline
      trimmedSpaceLine := strings.TrimSpace(line)
      if len(trimmedSpaceLine) > 0 && trimmedSpaceLine[0] == '#' {
        continue
      }
      if strings.ContainsAny(line, ":=") {
        splitLine := strings.Split(line, ":=")
        if splitLine != nil && len(splitLine) == 2 {
          resolvedValue := splitLine[1]
          if strings.ContainsAny(splitLine[1], "#") {
            resolvedValue = resolvedValue[:strings.LastIndex(resolvedValue, "#")]
          }
          for strings.ContainsAny(resolvedValue, "$(") {
            mkVarIndex := strings.Index(resolvedValue, "$(")
            mkVarFromIndexRstStr := resolvedValue[mkVarIndex:]
            mkVarEndIndex := strings.Index(mkVarFromIndexRstStr, ")")
            mkVarInsideVar := resolvedValue[mkVarIndex:mkVarIndex+mkVarEndIndex+1]
            if strings.ContainsAny(mkVarInsideVar, " ") {
              if debug {
                fmt.Println("Line", linenum, "contains spaces, skipping")
              }
              continue
            }
            if debug {
              fmt.Println("Found var", mkVarInsideVar, "inside var", splitLine[0])
            }
            resolvedValue = strings.Replace(resolvedValue,
                            mkVarInsideVar,
                            GetMakeVar(resolvedValue[mkVarIndex+2:mkVarIndex+mkVarEndIndex], ""),
                            1)
          }
          splitLineClean := []string{
            strings.TrimSpace(splitLine[0]),
            strings.TrimSpace(resolvedValue),
          }
          makevars = append(makevars, splitLineClean)
          if debug {
            fmt.Println("Added makevar '"+splitLineClean[0]+"' with value '"+splitLineClean[1]+"'")
          }
        }
      }
      if err == io.EOF {
        fmt.Println("Processed makevar file", makevarFiles[i])
        return 0
      } else if err != nil {
        panic(fmt.Sprintf("Error reading makevar file ", err))
      }
    }
  }
  return 1
}
