/*******************************************************************************
 * Copyright (c) 2022 Red Hat, Inc.
 * Distributed under license by Red Hat, Inc. All rights reserved.
 * This program is made available under the terms of the
 * Eclipse Public License v2.0 which accompanies this distribution,
 * and is available at http://www.eclipse.org/legal/epl-v20.html
 *
 * Contributors:
 * Red Hat, Inc.
 ******************************************************************************/
package command

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/moby/buildkit/frontend/dockerfile/parser"
	"github.com/redhat-developer/docker-openshift-analyzer/pkg/utils"
)

type Run struct{}

type runResultKeyType struct{}

var runResultKey runResultKeyType

func (r Run) Analyze(ctx context.Context, node *parser.Node, source utils.Source, line Line) context.Context {

	// let's split the run command by &&. E.g chmod 070 /app && chmod 070 /app/routes && chmod 070 /app/bin
	splittedCommands := strings.Split(node.Value, "&&")
	var results []Result
	for _, command := range splittedCommands {
		if r.isChmodCommand(command) {
			result := r.analyzeChmodCommand(command, source, line)
			if result != nil {
				results = append(results, *result)
			}
		} else if r.isChownCommand(command) {
			result := r.analyzeChownCommand(command, source, line)
			if result != nil {
				results = append(results, *result)
			}
		} else if r.isSudoOrSuCommand(command) {
			result := r.analyzeSudoAndSuCommand(command, source, line)
			if result != nil {
				results = append(results, *result)
			}
		}
	}
	return context.WithValue(ctx, runResultKey, results)
}

func (r Run) PostProcess(ctx context.Context) []Result {
	result := ctx.Value(runResultKey)
	if result == nil {
		return nil
	}
	return result.([]Result)
}

func (r Run) isSudoOrSuCommand(s string) bool {
	return IsCommand(s, "sudo") || IsCommand(s, "su")
}

func (r Run) analyzeSudoAndSuCommand(s string, source utils.Source, line Line) *Result {
	re := regexp.MustCompile(`(\s+|^)(sudo|su)\s+`)

	match := re.FindStringSubmatch(s)
	if len(match) > 0 {
		return &Result{
			Name:     "Use of sudo/su command",
			Status:   StatusFailed,
			Severity: SeverityMedium,
			Description: fmt.Sprintf(`sudo/su command used in '%s' %s could cause an unexpected behavior. 
		In OpenShift, containers are run using arbitrarily assigned user ID and elevating privileges could lead 
		to an unexpected behavior`, s, GenerateErrorLocation(source, line)),
		}
	}
	return nil
}

func (r Run) isChownCommand(s string) bool {
	return IsCommand(s, "chown")
}

/*
	to be tested on

chown -R node:node /app
chown --recursive=node:node
chown +x test
RUN chown -R $ZOOKEEPER_USER:$HADOOP_GROUP $ZOOKEEPER_LOG_DIR
chown -R 1000:1000 /app
chown 1001 /deployments/run-java.sh
chown -h 501:20 './AirRun Updates'
*/
func (r Run) analyzeChownCommand(s string, source utils.Source, line Line) *Result {
	re := regexp.MustCompile(`(\$*\w+)*:(\$*\w+)`)

	match := re.FindStringSubmatch(s)
	if len(match) == 0 {
		return nil // errors.New("unable to find any group set by the chown command")
	}
	group := match[len(match)-1]
	if strings.ToLower(group) != "root" && group != "0" {
		return &Result{
			Name:     "Owner set",
			Status:   StatusFailed,
			Severity: SeverityMedium,
			Description: fmt.Sprintf(`owner set on %s %s could cause an unexpected behavior. 
			In OpenShift the group ID must always be set to the root group (0)`, s, GenerateErrorLocation(source, line)),
		}
	}
	return nil
}

func (r Run) isChmodCommand(s string) bool {
	return IsCommand(s, "chmod")
}

func (r Run) analyzeChmodCommand(s string, source utils.Source, line Line) *Result {
	re := regexp.MustCompile(`chmod\s+(\d+)\s+(.*)`)
	match := re.FindStringSubmatch(s)
	if len(match) == 0 {
		return nil
	}
	if len(match) != 3 {
		return &Result{
			Name:        "Syntax error",
			Status:      StatusFailed,
			Severity:    SeverityCritical,
			Description: fmt.Sprintf("unable to fetch args of chmod command %s. Is it correct?", GenerateErrorLocation(source, line)),
		}
	}
	permission := match[1]
	if len(permission) != 3 {
		return &Result{
			Name:        "Syntax error",
			Status:      StatusFailed,
			Severity:    SeverityCritical,
			Description: fmt.Sprintf("unable to fetch args of chmod command %s. Is it correct?", GenerateErrorLocation(source, line)),
		}
	}
	groupPermission := permission[1:2]
	if groupPermission != "7" {
		proposal := fmt.Sprintf("Is it an executable file? Try updating permissions to %s7%s", permission[0:1], permission[2:3])
		if groupPermission != "6" {
			proposal += fmt.Sprintf(" otherwise set it to %s6%s", permission[0:1], permission[2:3])
		}
		return &Result{
			Name:     "Permission set",
			Status:   StatusFailed,
			Severity: SeverityMedium,
			Description: fmt.Sprintf("permission set on %s %s could cause an unexpected behavior. %s\n"+
				"Explanation - in Openshift, directories and files need to be read/writable by the root group and "+
				"files that must be executed should have group execute permissions", s, GenerateErrorLocation(source, line), proposal),
		}
	}

	return nil
}
